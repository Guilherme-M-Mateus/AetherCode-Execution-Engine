## Summary

<!-- What does this PR do? One or two sentences. -->

## Changes

<!-- Bullet list of the meaningful changes. -->

-

## Testing

<!-- How was this tested? Unit tests / integration / manual curl? -->

- [ ] `go test -race -count=1 ./...` passes locally
- [ ] Relevant tests added or updated
- [ ] `gofmt -s -l .` reports no unformatted files
- [ ] `go vet ./...` is clean

## Security checklist

<!-- Check all that apply. -->

- [ ] No secrets or credentials in code / config
- [ ] User input is parameterised (no raw SQL interpolation)
- [ ] New HTTP endpoints validate Content-Type and body size
- [ ] Auth/authz impact reviewed (especially for unauthenticated paths)
- [ ] No new dependencies introduced without review

## Related issues

<!-- Closes #issue_number -->
