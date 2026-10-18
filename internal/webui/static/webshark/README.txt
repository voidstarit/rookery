WebShark NG v3.0.0 (vendored static browser UI)
Source: https://github.com/hepictel/webshark
Project page: https://hepictel.github.io/webshark/
License: GPL-2.0 (see LICENSE.txt)

Built from the upstream UI with:
  npm ci
  npx ng build --configuration production --source-map=false --optimization=true

This copy is served by the rookery API at /webshark/. The Go handler maps
wiregasm.wasm and wiregasm.data requests to the .gz assets shipped by the
upstream build. The upstream ffmpeg-core.wasm is intentionally omitted, so
RTP audio playback is unavailable; packet dissection and pcap download work.

Roboto and Material Icons woff2 files are vendored under fonts/ so the UI
renders icons without external font CDNs.
