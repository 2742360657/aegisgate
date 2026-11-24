# AegisGate contributor instructions

## Local coordination

Before making changes, locate an optional, untracked local context file:

```sh
git rev-parse --git-path agent-context.md
```

If that file exists, read it and follow its coordination entry points. It is machine-local metadata: never copy its contents or referenced private materials into the source tree, commits, issues, or pull requests. If it is absent, use the public documentation and the current request; do not infer a private owner's goals or learning progress.

## Engineering

- Use Go and net/http. The proxy uses httputil.ReverseProxy with an explicitly configured Transport.
- Keep changes small and explain concurrency, lifecycle, and resource-boundary decisions.
- Configure network deadlines and bounded body handling. Keep credentials and business payloads out of default logs.
- Verify changed behavior with appropriate tests. Concurrent changes require race checks; performance claims require reproducible measurements.
- Add dependencies only when needed. Keep examples portable and use loopback addresses or clear placeholders.
- Preserve existing work. Do not implement unrelated future features or exercises assigned to someone else.
- Read CONTRIBUTING.md when present for commit, review, and publication conventions.
- Commit coherent changes with accurate messages. Do not fabricate author identities, dates, learning achievements, or commit volume.
- Publishing, pushing, changing visibility, and deployment require authorization in the current task. A request to edit or commit locally is not publication authorization.
