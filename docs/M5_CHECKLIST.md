# M5 Deployment Checklist

Goal: run the first real load test against infra that isn't localhost —
coordinator + Redis + the guinea-pig app on an Oracle Cloud Always Free VM,
with the worker running as an ephemeral GitHub Actions job. This is the one
V1 step that needs Henry directly — cloud account creation requires
identity/payment-verification info I don't have and shouldn't have.

## Why GitHub Actions for the worker, not a second cloud account

We already have `gh` authenticated and a public repo (unlimited free Actions
minutes on public repos). Using Actions as the ephemeral worker means only
**one** new account is needed (Oracle), not two.

## Steps for Henry

1. **Sign up for Oracle Cloud** (cloud.oracle.com) if you don't have an
   account. You'll need: email, phone verification, and a credit card —
   Oracle requires this for identity verification even for Always Free
   resources. You won't be charged unless you explicitly upgrade.
2. **Pick a home region** during signup close to you. Per research done in
   this project (July 2026): `us-ashburn-1` and `us-phoenix-1` have had the
   most consistent Always Free ARM capacity; other regions have seen
   shortages.
3. **Create an Always Free compute instance**:
   - Shape: `VM.Standard.A1.Flex` (ARM, Always Free) — 1 OCPU / 6GB is
     plenty
   - Image: Ubuntu (latest LTS)
   - Note: Oracle's Always Free ARM capacity has been documented as flaky
     — if creation fails with an "out of capacity" error, try a different
     Availability Domain in your region, or retry later
4. **Once the instance is running, get**:
   - Its public IP address
   - SSH access (the key pair Oracle generates, or your own public key
     added to the instance)
5. **Open these ports** in the VM's security list / network security
   group:
   - 22 (SSH — usually open by default)
   - 6379 (Redis) — restrict source to your own IP if possible rather than
     `0.0.0.0/0`
   - 8081 (guinea-pig app) — same
6. **Come back and tell me**: the public IP, and confirm `ssh <user>@<ip>`
   works from your machine. I'll take it from there.

## What happens once you hand me access

- Cross-compile coordinator/worker/guineapig for `linux/arm64`
- Deploy Redis (via the existing `docker-compose.yml`) + coordinator +
  guineapig to the VM
- Write a GitHub Actions workflow that runs the worker binary as an
  ephemeral job, pointed at the VM's Redis over the network
- Run the first real remote load test end to end and verify live results
  stream back correctly — not localhost this time
- Mark V1 complete in SCOPE.md/PROGRESS.md
