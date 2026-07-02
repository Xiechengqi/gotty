package server

import "embed"

//go:embed http_tunnel_client_bin
var embeddedHTTPTunnelClientFS embed.FS

const embeddedHTTPTunnelClientPath = "http_tunnel_client_bin/http-tunnel-client"
