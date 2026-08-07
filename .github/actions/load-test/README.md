# Sentry Load Test action

Load-test a URL — typically a PR preview deployment — with [Sentry
Load](https://github.com/HenryWinNguyen/sentry-load) and report the
results back on the pull request.

## Usage

```yaml
- name: Load test the preview deployment
  uses: HenryWinNguyen/sentry-load/.github/actions/load-test@main
  with:
    url: ${{ steps.deploy.outputs.preview-url }}
    coordinator-url: https://your-sentry-load-coordinator.example.com
    token: ${{ secrets.SENTRY_LOAD_TOKEN }}
```

Get a token by logging into your coordinator's dashboard (`GET
/auth/github/login`) and storing the returned bearer token as a repo
secret — see the main project's `CLAUDE.md` for the full API reference.
The target URL needs to already be verified or on the coordinator's
allowlist; an unverified target is rejected with a 403, same as it would
be from the dashboard.

## Inputs

| Name               | Required | Default            | Description                                             |
| ------------------ | -------- | ------------------ | --------------------------------------------------------- |
| `url`               | yes      |                     | The URL to load-test.                                     |
| `coordinator-url`    | yes      |                     | Base URL of your Sentry Load coordinator.                 |
| `token`              | yes      |                     | Bearer token for your Sentry Load account.                 |
| `vus`                | no       | `20`                | Concurrent virtual users.                                  |
| `duration-seconds`    | no       | `30`                | Test duration, in seconds.                                 |
| `ramp-pattern`        | no       | `steady`            | `"steady"` or `"ramp"`.                                    |
| `worker-count`        | no       | `1`                 | Number of workers to fan the test across.                  |
| `comment-on-pr`       | no       | `true`              | Post a summary comment on the pull request.                |
| `github-token`        | no       | `${{ github.token }}` | Token used to post the PR comment.                       |

## Outputs

| Name              | Description                                              |
| ----------------- | ---------------------------------------------------------- |
| `test-id`          | The Sentry Load test ID.                                    |
| `total-requests`    | Total requests across all workers.                          |
| `total-errors`      | Total errors across all workers.                            |
| `combined-rps`      | Combined requests/second.                                   |
| `circuit-broken`    | `true` if the test was aborted early due to a spiking error rate. |
| `report-url`        | Public report link, if the coordinator has sharing enabled (Postgres configured). |

## Example: fail the build on a high error rate

```yaml
- name: Load test the preview deployment
  id: load-test
  uses: HenryWinNguyen/sentry-load/.github/actions/load-test@main
  with:
    url: ${{ steps.deploy.outputs.preview-url }}
    coordinator-url: https://your-sentry-load-coordinator.example.com
    token: ${{ secrets.SENTRY_LOAD_TOKEN }}

- name: Fail if the target couldn't handle the load
  if: steps.load-test.outputs.circuit-broken == 'true'
  run: exit 1
```
