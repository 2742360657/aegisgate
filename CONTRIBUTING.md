# Contributing

## Development

Use the commands in README.md. For a normal change, run format checks, go vet and the relevant tests. Concurrent changes require race detection. Run the real-process smoke test when proxy or shutdown behavior changes.

Prefer one coherent behavior change with its necessary tests per commit. Separate unrelated changes. Describe what changed and why; useful prefixes include feat, fix, test, refactor, perf, docs and chore. A performance improvement needs a reproducible comparison, not just an implementation change.

Do not manufacture commit volume, backdate work, or rewrite attribution to imply work or learning that did not occur. Document AI-assisted changes accurately when that information is material to review.

## Public source and local context

The repository contains code, tests, portable configuration examples, build tools, usage instructions and technical reasoning. Machine-local coordination, private goals, study records, task ownership, session handoffs, credentials and personal files belong outside the tracked source tree.

AGENTS.md describes how to locate an optional local coordination entry point. Its contents and referenced materials must remain local. A fresh clone must still build and run using only the public instructions.

Use relative paths, loopback addresses or obvious placeholders in examples. Do not commit actual machine paths, credentials, local environment files, logs or database data. Keep technical test results and measured benchmarks reproducible and independent of private task records.

## Before committing

Review the exact staged change, including filenames and documentation:

```bash
git diff --cached --stat
git diff --cached
python3 scripts/check_public.py --staged
```

Optionally enable the repository-local hooks:

```bash
bash scripts/install-hooks.sh
```

The pre-commit hook scans the index, so an unstaged cleanup cannot hide content that is still staged. The pre-push hook scans the full ancestry of the proposed local tips, so deleting private text from the latest version does not conceal it in older commits.

These checks detect common local files, machine paths, profile fragments and a few credential formats. They do not prove that all secrets, personal information, screenshots, binaries, ignored files or external material are safe. Review content and author metadata manually. Hooks are local and can be bypassed; a clone must install them separately.

## First publication and later pushes

1. Check the full reachable history, not only the final files:

   ```bash
   python3 scripts/check_public.py --history
   git log --format=fuller
   git remote -v
   ```

2. Review author and committer names and email addresses. Use an identity you intend to make public; a GitHub-provided noreply address can protect your email and associate commits with your account. Local placeholder emails need review. Changing Git configuration only affects future commits.
3. Confirm the destination account, repository visibility and publication authorization. A private remote is still an upload.
4. Push the Git repository when you want to preserve its existing commit chain. Uploading only the final files through a web form does not transfer that chain. If a source archive is needed, export tracked files with git archive instead of zipping the entire working directory, which can include ignored files and local Git metadata.
5. Run the applicable tests and inspect staged content before later commits; run the history check again before publishing.

Do not push automatically merely because a local task or commit finished. Do not rewrite shared history or force-push as routine cleanup.
