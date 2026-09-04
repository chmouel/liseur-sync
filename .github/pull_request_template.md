## Summary

<!-- What does this change do, and why? Link related issues with "Fixes #123". -->

## Changes

-

## Testing

- [ ] `go test -race ./...`
- [ ] `go vet ./...`
- [ ] `gofmt -l ./cmd ./internal` reports no files
- [ ] If `.templ` files changed: `go tool templ generate ./internal/webui/` and committed generated output
- [ ] If `docs/openapi.yaml` changed: `npx -y @redocly/cli@latest lint docs/openapi.yaml --format stylish`
- [ ] Manually tested the affected API, adapter, web UI, or deployment behavior, if applicable

## Screenshots, logs, or API examples

<!-- Include relevant evidence for UI or protocol changes. Remove credentials,
tokens, private URLs, personal information, and copyrighted book files. -->

## Checklist

- [ ] I have kept this change focused and documented user-facing behavior.
- [ ] I have added or updated tests where needed.
- [ ] I have updated related API and integration documentation where needed.
- [ ] I have documented deployment or migration impact where applicable.
- [ ] I have not included secrets, private data, copyrighted book files, or generated build artifacts.
