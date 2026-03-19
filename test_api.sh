#!/bin/bash
#
# GoTTY REST API 冒烟测试脚本
#
# 用法:
#   ./test_api.sh <base_url> [api_token]
#
# 示例:
#   ./test_api.sh http://192.168.1.14:2222
#   ./test_api.sh http://192.168.1.14:2222 my-secret-token
#

set -euo pipefail

BASE_URL="${1:?用法: $0 <base_url> [api_token]}"
API_TOKEN="${2:-}"

# 去掉末尾斜杠
BASE_URL="${BASE_URL%/}"
API="${BASE_URL}/api/v1"

# 构建 auth header
AUTH_HEADER=""
if [ -n "$API_TOKEN" ]; then
    AUTH_HEADER="Authorization: Bearer ${API_TOKEN}"
fi

# 颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
NC='\033[0m'

PASS=0
FAIL=0
TOTAL=0

curl_cmd() {
    local args=(-SsL --max-time 15)
    if [ -n "$AUTH_HEADER" ]; then
        args+=(-H "$AUTH_HEADER")
    fi
    curl "${args[@]}" "$@"
}

assert_eq() {
    local label="$1" expected="$2" actual="$3"
    TOTAL=$((TOTAL + 1))
    if [ "$expected" = "$actual" ]; then
        echo -e "  ${GREEN}PASS${NC} $label (=$expected)"
        PASS=$((PASS + 1))
    else
        echo -e "  ${RED}FAIL${NC} $label (expected=$expected, got=$actual)"
        FAIL=$((FAIL + 1))
    fi
}

assert_not_empty() {
    local label="$1" actual="$2"
    TOTAL=$((TOTAL + 1))
    if [ -n "$actual" ]; then
        echo -e "  ${GREEN}PASS${NC} $label (non-empty)"
        PASS=$((PASS + 1))
    else
        echo -e "  ${RED}FAIL${NC} $label (empty)"
        FAIL=$((FAIL + 1))
    fi
}

assert_contains() {
    local label="$1" haystack="$2" needle="$3"
    TOTAL=$((TOTAL + 1))
    if echo "$haystack" | grep -qF "$needle"; then
        echo -e "  ${GREEN}PASS${NC} $label (contains '$needle')"
        PASS=$((PASS + 1))
    else
        echo -e "  ${RED}FAIL${NC} $label (missing '$needle')"
        FAIL=$((FAIL + 1))
    fi
}

assert_http_status() {
    local label="$1" expected="$2" actual="$3"
    TOTAL=$((TOTAL + 1))
    if [ "$expected" = "$actual" ]; then
        echo -e "  ${GREEN}PASS${NC} $label (HTTP $expected)"
        PASS=$((PASS + 1))
    else
        echo -e "  ${RED}FAIL${NC} $label (expected HTTP $expected, got $actual)"
        FAIL=$((FAIL + 1))
    fi
}

# ============================================================
echo -e "${CYAN}[1/7] GET /api/v1/status${NC}"
# ============================================================
RESP=$(curl_cmd "${API}/status")
STATE=$(echo "$RESP" | jq -r '.state')
CLIENTS=$(echo "$RESP" | jq -r '.connected_clients')
COLS=$(echo "$RESP" | jq -r '.terminal_size.cols')
ROWS=$(echo "$RESP" | jq -r '.terminal_size.rows')

assert_eq "state is idle" "idle" "$STATE"
assert_not_empty "connected_clients" "$CLIENTS"
assert_not_empty "terminal_size.cols" "$COLS"
assert_not_empty "terminal_size.rows" "$ROWS"

# ============================================================
echo -e "${CYAN}[2/7] POST /api/v1/exec — 基本命令${NC}"
# ============================================================
RESP=$(curl_cmd -X POST "${API}/exec" -d '{"command":"echo GOTTY_SMOKE_TEST_OK","timeout":5}')
EXIT_CODE=$(echo "$RESP" | jq -r '.exit_code')
EXEC_ID=$(echo "$RESP" | jq -r '.exec_id')
OUTPUT=$(echo "$RESP" | jq -r '.output')
TIMED_OUT=$(echo "$RESP" | jq -r '.timed_out')

assert_eq "exit_code" "0" "$EXIT_CODE"
assert_not_empty "exec_id" "$EXEC_ID"
assert_contains "output contains marker" "$OUTPUT" "GOTTY_SMOKE_TEST_OK"
assert_eq "timed_out" "false" "$TIMED_OUT"

# ============================================================
echo -e "${CYAN}[3/7] POST /api/v1/exec — 失败命令 (exit code)${NC}"
# ============================================================
RESP=$(curl_cmd -X POST "${API}/exec" -d '{"command":"ls /nonexistent_path_12345","timeout":5}')
EXIT_CODE=$(echo "$RESP" | jq -r '.exit_code')

assert_eq "exit_code non-zero" "2" "$EXIT_CODE"

# ============================================================
echo -e "${CYAN}[4/7] POST /api/v1/input — 模拟键盘输入${NC}"
# ============================================================
# 输入文本
RESP=$(curl_cmd -X POST "${API}/input" -d '{"type":"text","data":"# smoke test input"}')
OK=$(echo "$RESP" | jq -r '.ok')
assert_eq "input text ok" "true" "$OK"

# 按回车
RESP=$(curl_cmd -X POST "${API}/input" -d '{"type":"key","data":"enter"}')
OK=$(echo "$RESP" | jq -r '.ok')
assert_eq "input enter ok" "true" "$OK"

# Ctrl+C
sleep 0.3
RESP=$(curl_cmd -X POST "${API}/input" -d '{"type":"ctrl","data":"c"}')
OK=$(echo "$RESP" | jq -r '.ok')
assert_eq "input ctrl+c ok" "true" "$OK"

# ============================================================
echo -e "${CYAN}[5/7] GET /api/v1/output/lines${NC}"
# ============================================================
sleep 0.5
RESP=$(curl_cmd "${API}/output/lines?n=10")
TOTAL_LINES=$(echo "$RESP" | jq -r '.total')
LINES_ARR=$(echo "$RESP" | jq -r '.lines | length')

assert_not_empty "total lines" "$TOTAL_LINES"
assert_not_empty "lines array" "$LINES_ARR"

# ============================================================
echo -e "${CYAN}[6/7] POST /api/v1/exec/stream — SSE 流式执行${NC}"
# ============================================================
sleep 1
SSE_OUTPUT=$(curl_cmd -X POST "${API}/exec/stream" -d '{"command":"echo STREAM_TEST_123","timeout":5}')
assert_contains "SSE has started event" "$SSE_OUTPUT" '"type":"started"'
assert_contains "SSE has completed event" "$SSE_OUTPUT" '"type":"completed"'
assert_contains "SSE output contains marker" "$SSE_OUTPUT" "STREAM_TEST_123"

# ============================================================
echo -e "${CYAN}[7/7] 错误场景测试${NC}"
# ============================================================

# 无效输入类型
HTTP_STATUS=$(curl_cmd -o /dev/null -w '%{http_code}' -X POST "${API}/input" -d '{"type":"invalid","data":"x"}')
assert_http_status "invalid input type → 400" "400" "$HTTP_STATUS"

# 空命令
HTTP_STATUS=$(curl_cmd -o /dev/null -w '%{http_code}' -X POST "${API}/exec" -d '{"command":""}')
assert_http_status "empty command → 400" "400" "$HTTP_STATUS"

# 错误 HTTP 方法
HTTP_STATUS=$(curl_cmd -o /dev/null -w '%{http_code}' -X GET "${API}/exec")
assert_http_status "GET /exec → 405" "405" "$HTTP_STATUS"

HTTP_STATUS=$(curl_cmd -o /dev/null -w '%{http_code}' -X GET "${API}/input")
assert_http_status "GET /input → 405" "405" "$HTTP_STATUS"

# Token 认证测试（仅当设置了 token 时）
if [ -n "$API_TOKEN" ]; then
    echo -e "${CYAN}  [Token 认证]${NC}"
    HTTP_STATUS=$(curl -SsL --max-time 5 -o /dev/null -w '%{http_code}' "${API}/status")
    assert_http_status "no token → 401" "401" "$HTTP_STATUS"

    HTTP_STATUS=$(curl -SsL --max-time 5 -o /dev/null -w '%{http_code}' -H "Authorization: Bearer wrong-token" "${API}/status")
    assert_http_status "wrong token → 401" "401" "$HTTP_STATUS"

    HTTP_STATUS=$(curl -SsL --max-time 5 -o /dev/null -w '%{http_code}' "${API}/status?token=${API_TOKEN}")
    assert_http_status "query token → 200" "200" "$HTTP_STATUS"
fi

# ============================================================
echo ""
echo -e "=============================="
echo -e " 结果: ${GREEN}${PASS} 通过${NC} / ${RED}${FAIL} 失败${NC} / ${TOTAL} 总计"
echo -e "=============================="

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
