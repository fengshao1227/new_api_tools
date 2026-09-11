# Top-ups production fix — 2026-09-11

## Scope

This record covers the NewAPI-Tool admin page at `https://beattool.fengshao1227.com/topups`.
The source of truth is this repository, `fengshao1227/new_api_tools`; production runs the
`beat-newapi-tools` container from `/home/ubuntu/beat-newapi-tools`.

## Root causes found in production

- The top-up API returned the raw ledger columns without a presentation projection. Hosted
  providers such as Dodo store wallet quota in `amount` (`5,000,000` quota units = `$10`), while
  Stripe stores the purchased USD amount. The table therefore rendered Dodo as `$5,000,000`.
- The admin table did not select `users.email`, so the user email column had no value.
- Payment rows have no universal currency column. Epay/Alipay/WeChat settle in CNY; Stripe,
  Dodo, PayPal, Creem and Waffo settle in USD. Treating every `money` value as CNY made the
  summary and Stripe revenue misleading.
- The growth trend is deliberately a first-time payer metric. User 499 paid successfully on
  2026-09-06 and paid again on 2026-09-10; showing `0` first-time payers on 2026-09-10 is correct.
  The label now says `首次付费用户` / `First-time paying users`.
- The growth SQL had one remaining inconsistency: revenue used the normalized success predicate,
  while the first-payer subquery compared `status = 'success'` literally. It now uses the same
  predicate, so `success`, `completed`, and legacy numeric `1` rows are counted consistently.

## Implemented behavior

- `/api/top-ups` adds `user_email`, `amount_usd`, `payment_currency`, and `money_usd` while
  retaining the raw ledger fields for audit and compatibility.
- Hosted quota is converted with `500000 quota = 1 USD`; CNY money is converted using the
  configured `CNY_PER_USD` rate (production default is 7).
- The table shows the actual payment in its confirmed currency and the acquired balance in USD.
- Aggregate cards and per-user income summaries use normalized USD fields.
- The table contains a dedicated email column and recognizes Dodo, PayPal and Creem labels.

## Validation

- `cd backend && go test ./...` passes.
- The production Docker build passes, including the frontend TypeScript/Vite build.
- GitHub Actions run `34549599043` passed all jobs, including `Deploy to production`.
- Production container reports `running healthy`.

## Deployment contract

`.github/workflows/build.yml` now builds both architectures and, on `main` push, SSHes to the
production host using repository secrets, checks out the pushed commit, builds
`beat-newapi-tools:local`, recreates the Tool container, and checks `/api/health`.

Required repository secrets:

- `DEPLOY_SSH_KEY`
- `DEPLOY_KNOWN_HOSTS`
- `DEPLOY_HOST`
- `DEPLOY_USER`
- `DEPLOY_PATH`

The current production commit is `f77b157`; the application fix is included through
`705bb3b`. Do not print or commit any secret values.
