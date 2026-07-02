This directory is embedded into release builds.

GitHub Actions downloads the matching http-tunnel-client binary to:

  server/http_tunnel_client_bin/http-tunnel-client

The downloaded binary is ignored by git. This placeholder keeps the embedded
directory present for local development builds.
