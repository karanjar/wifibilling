# WiFi Billing Server (Go + Gin)

Server-side billing for a paid WiFi hotspot: customers pick a plan on your captive portal, pay via **M-Pesa STK Push** (or redeem a **voucher**), and the router asks this server whether a device may go online.

## Run

```bash
cd wifi-billing
cp .env.example .env        # fill in values
go mod tidy                 # downloads deps and writes go.sum
set -a; source .env; set +a
go run .
```

M-Pesa needs a **public HTTPS** callback URL. While developing, expose your port with `ngrok http 8080` and set `MPESA_CALLBACK_BASE` to the ngrok URL.

## Flow

1. Router redirects an unauthenticated device to your portal, passing the device MAC.
2. Portal calls `GET /api/v1/plans`, user picks one.
3. Portal calls `POST /api/v1/pay/mpesa`; the phone shows the PIN prompt.
4. Safaricom calls `/api/v1/mpesa/callback/:secret`; server verifies the amount and creates a session.
5. Portal polls `GET /api/v1/pay/status/:checkout_id` until `completed`, then tells the router to re-check.
6. Router calls `GET /api/v1/gateway/check?mac=...` (or syncs `/gateway/active`) and whitelists the MAC with the returned speed limits.
7. Router periodically posts usage to `/gateway/usage`; a background worker expires sessions every 30 s.

Buying again while connected **stacks** the new time onto the existing session.

## API

### Public
| Method | Path | Body / Query |
|---|---|---|
| GET | `/api/v1/plans` | |
| POST | `/api/v1/pay/mpesa` | `{plan_id, phone, mac}` |
| GET | `/api/v1/pay/status/:checkout_id` | |
| POST | `/api/v1/voucher/redeem` | `{code, mac}` |
| GET | `/api/v1/session?mac=` | |
| POST | `/api/v1/mpesa/callback/:secret` | Safaricom only |

### Gateway (header `X-API-Key`)
| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/gateway/check?mac=` | `{allowed, remaining_seconds, down_kbps, up_kbps}` |
| GET | `/api/v1/gateway/active` | all currently authorised MACs |
| POST | `/api/v1/gateway/usage` | `{mac, bytes}`; closes session at data cap |

### Admin (header `Authorization: Bearer <jwt>`)
`POST /admin/login` returns the token, then:

`GET /admin/stats`, `GET|POST /admin/plans`, `PUT|DELETE /admin/plans/:id`, `GET|POST /admin/vouchers`, `GET /admin/payments`, `GET /admin/sessions`, `POST /admin/sessions/:id/revoke`

## Quick test

```bash
curl localhost:8080/api/v1/plans

TOKEN=$(curl -s localhost:8080/api/v1/admin/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"YOUR_ADMIN_PASS"}' | jq -r .token)

curl -X POST localhost:8080/api/v1/admin/vouchers \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"plan_id":1,"count":5}'

curl -X POST localhost:8080/api/v1/voucher/redeem \
  -H 'Content-Type: application/json' \
  -d '{"code":"<CODE>","mac":"AA:BB:CC:DD:EE:FF"}'

curl -H "X-API-Key: YOUR_GATEWAY_KEY" "localhost:8080/api/v1/gateway/check?mac=AA:BB:CC:DD:EE:FF"
```

## Router integration

This server is the billing brain; the router enforces access. Common options:

- **MikroTik**: a script (`/tool fetch`) on a scheduler calls `/gateway/active` and adds hotspot IP-bindings / user profiles with the rate limits. Or use the hotspot login page to hit `/gateway/check`.
- **OpenWrt + openNDS / CoovaChilli**: point the binauth script at `/gateway/check`.
- **FreeRADIUS**: add a small `rlm_rest` module that calls `/gateway/check` for authorization.

## Before going to production

- Switch `MPESA_ENV=production` and use your Paybill/Till credentials (for a Till, change `TransactionType` to `CustomerBuyGoodsOnline` in `mpesa`).
- Run behind HTTPS (Caddy/Nginx) and restrict CORS to your portal origin.
- Add rate limiting on `/pay/mpesa` and `/voucher/redeem` to stop abuse and voucher guessing.
- Optionally allowlist Safaricom's callback IPs at your proxy, in addition to the secret path.
- Move from SQLite to PostgreSQL if you expect high concurrency (swap the GORM driver in `main.go`).
# wifibilling
