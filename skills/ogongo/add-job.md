# add-job

Add a background job to an OgonGo project and verify it enqueues +
processes + retries + dead-letter-queues correctly.

## When to use

- A new background task is required (e.g. welcome_email, daily_report).
- The job has retries + DLQ semantics.
- The job does NOT require a new model (use add-model for that).

## Preconditions

- .ogon/ogon.json exists. If not, run `ogon agent dump --write`.
- The jobs queue is configured (run `ogon doctor` first).

## Steps

1. Plan the job:
   `ogon mcp tools/call ogon.gen {"kind":"job","name":"welcome_email"}`
2. Review the plan. The plan includes app/jobs/welcome_email_job.go.
3. Apply the plan via the CLI (CI requires --yes):
   `ogon gen job welcome_email --yes`
4. Edit app/jobs/welcome_email_job.go to fill the Run body.
5. Run the pre-commit gate:
   `ogon check`
6. Run affected tests:
   `ogon test --filter ./app/jobs/...`

## Verify

- ogon check exits 0.
- ogon test passes.
- Enqueue + process + retry + DLQ can be observed in <=5 lines of test.
- ogon explain jobs returns the job's queue + retry policy + DLQ topic.

## Escape hatches

- To override the retry policy, set it in app/jobs/welcome_email_job.go.
- To enqueue from outside the framework, publish to the queue topic
  directly (the broker interface is public).
