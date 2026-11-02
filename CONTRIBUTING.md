# Contributing

Bug reports, doc fixes and small improvements are welcome. For anything bigger, open an issue first so we can agree on the approach before you spend time on it.

## Run the checks

```bash
go test ./...
```

Some packages need extra services or tools. If a package fails for a reason unrelated to your change, say so in the PR and name the package.

## Pull requests

Keep each PR to one change. Say what you ran to check it. CI has to pass before merge. Issues labelled "good first issue" are a good place to start.
