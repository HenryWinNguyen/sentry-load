# Replacing ngrok with a Cloudflare Tunnel (M21, not done yet)

The coordinator is exposed over HTTPS through ngrok's free tier. That works,
but it has costs: every request needs the `ngrok-skip-browser-warning`
header or it gets an HTML interstitial, which is why the dashboard sends it
and the coordinator's CORS config allows it. There's also a monthly
bandwidth cap, and an ngrok-branded hostname in the badge embed URL.

A Cloudflare named tunnel fixes all three for $0 in Cloudflare fees.
**The blocker is that it needs a domain whose DNS is on Cloudflare**, so
this is a money/account decision (a domain is ~$10/yr, e.g. via
Cloudflare Registrar at cost). Cloudflare's no-account "quick tunnels"
(`*.trycloudflare.com`) don't help here: the hostname changes on every
restart, so it's worse than ngrok's static free domain.

## Steps, once a domain is on Cloudflare

On the Oracle VM:

```bash
# 1. Install cloudflared (check `uname -m`: amd64 vs arm64)
curl -L -o cloudflared.deb https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb
sudo dpkg -i cloudflared.deb

# 2. Authenticate (prints a URL to open in your browser, then pick the domain)
cloudflared tunnel login

# 3. Create the tunnel and point a hostname at it
cloudflared tunnel create sentry-load
cloudflared tunnel route dns sentry-load api.<your-domain>

# 4. Config: everything for that hostname goes to the local coordinator.
#    WebSockets (GET /tests/{id}/live) work through the tunnel with no
#    extra config.
mkdir -p ~/.cloudflared
cat > ~/.cloudflared/config.yml <<EOF
tunnel: sentry-load
credentials-file: /home/<user>/.cloudflared/<tunnel-id>.json
ingress:
  - hostname: api.<your-domain>
    service: http://localhost:8080
  - service: http_status:404
EOF

# 5. Run it as a service so it survives reboots (unlike the nohup'd ngrok)
sudo cloudflared service install
sudo systemctl enable --now cloudflared
```

Then point everything else at the new hostname:

1. **Vercel**: set `NEXT_PUBLIC_COORDINATOR_URL=https://api.<your-domain>`
   and redeploy (it's baked in at build time).
2. **GitHub OAuth App** (production one): change the callback URL to
   `https://api.<your-domain>/auth/github/callback`.
3. **Coordinator env on the VM**: `GITHUB_REDIRECT_URL` to the same
   callback URL, then restart the coordinator.
4. Check: log in through the dashboard, run a Quick Check, open its share
   link and badge.
5. Once it's all confirmed working, stop ngrok. The `ngrok-skip-browser-warning`
   header in `dashboard/src/lib/api.ts` and the coordinator's CORS allowlist
   are harmless against any other host and can be removed afterwards.

Badges already embedded in someone's README point at the old ngrok hostname
and will break once ngrok is gone. Nobody outside this project uses them
yet, but that's the one thing this change can't migrate.
