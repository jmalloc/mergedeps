# mergedeps

This is a quick-and-dirty CLI tool for quickly merging Dependabot PRs in bulk.

- Install via `go install github.com/jmalloc/mergedeps/cmd/mergedeps@latest`
- Set a `GITHUB_TOKEN` environment variable to a personal-access-token that has merge permissions.
- Run `mergedeps <org name>`

It will prompt you to approve each version of a given dependency. For each
version that you approve it instructs dependabot to merge any open PRs for that
version across all repositories in the organisation.
