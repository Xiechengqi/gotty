import type {TerminalStatePayload} from './webtty';

// Text only: shared by native GoTTY and Hub tooltips, never changes geometry.
export function describeGeometry(state: TerminalStatePayload | undefined, local: {columns: number; rows: number} | undefined, locale = 'en'): string {
    if (!state) return '';
    const zh = locale.startsWith('zh');
    const devices: Record<string, string> = zh ? {desktop: '桌面端', tablet: '平板', mobile: '手机'} : {desktop: 'Desktop', tablet: 'Tablet', mobile: 'Phone'};
    const reasons: Record<string, string> = zh ? {
        'adaptive-initial': '首次选择', 'adaptive-preferred': '更合适的视口', 'adaptive-hidden': '原控制端已隐藏',
        'adaptive-expired': '原控制端续约过期', 'adaptive-disconnected': '原控制端已断开',
    } : {
        'adaptive-initial': 'Initial selection', 'adaptive-preferred': 'Preferred viewport', 'adaptive-hidden': 'Previous controller hidden',
        'adaptive-expired': 'Previous controller expired', 'adaptive-disconnected': 'Previous controller disconnected',
    };
    const lines = [ `${zh ? '共享网格' : 'Shared grid'}: ${state.activeCols} × ${state.activeRows}` ];
    if (local) lines.push(`${zh ? '本地可用' : 'Local capacity'}: ${local.columns} × ${local.rows}`);
    if (state.policy) lines.push(`${zh ? '尺寸策略' : 'Size policy'}: ${state.policy}`);
    if (state.leaderClientId) lines.push(`${zh ? '尺寸来源' : 'Size controller'}: ${devices[state.leaderDevice ?? ''] ?? (zh ? '客户端' : 'Client')} (${state.leaderClientId})${state.leaderVisible === false ? (zh ? ' · 暂不可见或离线' : ' · Hidden or offline') : ''}`);
    if (state.controlReason && reasons[state.controlReason]) lines.push(`${zh ? '选择原因' : 'Selection reason'}: ${reasons[state.controlReason]}`);
    return lines.join('\n');
}
