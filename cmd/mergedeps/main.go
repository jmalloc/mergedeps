package main

import (
	"bufio"
	"context"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/go-github/v32/github"
	"golang.org/x/oauth2"
	"golang.org/x/sync/errgroup"
)

const dependabotUserID = 49699333

var (
	titleRegex = regexp.MustCompile(`^Bump (.+) from .+ to (.+)$`)
	stdin      = bufio.NewReader(os.Stdin)
)

func main() {
	rand.Seed(time.Now().UnixNano())

	ctx := context.Background()

	if len(os.Args) != 2 {
		fmt.Println("usage: mergedeps <github org>")
		os.Exit(1)
	}

	if err := run(ctx, os.Args[1]); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, org string) error {
	cli := github.NewClient(
		oauth2.NewClient(
			ctx,
			oauth2.StaticTokenSource(
				&oauth2.Token{
					AccessToken: os.Getenv("GITHUB_TOKEN"),
				},
			),
		),
	)

	pullRequests := make(chan *github.PullRequest)
	g, ctx := errgroup.WithContext(ctx)

	// Start a goroutine that queries all of the repos in the org ...
	g.Go(func() error {
		defer close(pullRequests)

		// Create another err group used for querying the PRs in each repo. We
		// need a separate err group so that we can wait for it in order to
		// defer closing of the channel until all queries are performed.
		g, ctx := errgroup.WithContext(ctx)

		if err := forEachRepo(ctx, cli, org, func(r *github.Repository) {
			// Start a goroutine to query the open PRs on each of the repos ...
			g.Go(func() error {
				return forEachPR(ctx, cli, r, func(pr *github.PullRequest) {
					// Send information about each discovered PR over the channel ...
					select {
					case <-ctx.Done():
					case pullRequests <- pr:
					}
				})
			})
		}); err != nil {
			return err
		}

		return g.Wait()
	})

	// Start another goroutine for reading of the channel ...
	g.Go(func() error {
		dependencies := map[string]bool{}

		// Start an errgroup to track goroutines that perform merges.
		g, ctx := errgroup.WithContext(ctx)

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case pr, ok := <-pullRequests:
				if !ok {
					// There's no more PRs, so just wait for all of the approved
					// merges to finish.
					fmt.Println("...")
					return g.Wait()
				}

				if len(dependencies) > 0 {
					continue
				}

				matches := titleRegex.FindStringSubmatch(pr.GetTitle())
				if len(matches) == 0 {
					return fmt.Errorf("PR title did match expected pattern: %s", pr.GetTitle())
				}

				key := matches[1] + "@" + matches[2]
				approved, known := dependencies[key]

				if !known {
					fmt.Println("")
					approved = confirm("Update %s to %s?", matches[1], matches[2])
					fmt.Println("")
					dependencies[key] = approved
				}

				ref := fmt.Sprintf("%s#%d", pr.GetBase().GetRepo().GetFullName(), pr.GetNumber())

				if approved {
					fmt.Printf("    MERGE %-30s  %s\n", ref, pr.GetTitle())
					g.Go(func() error {
						return merge(ctx, cli, pr)
					})
				} else {
					fmt.Printf("    SKIP  %-30s  %s\n", ref, pr.GetTitle())
				}
			}
		}
	})

	return g.Wait()
}

// forEachRepo calls fn(repo) for each unarchived repo in the given organisation
// for which the current user can merge PRs.
func forEachRepo(
	ctx context.Context,
	cli *github.Client,
	org string,
	fn func(*github.Repository),
) error {
	opts := github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	for {
		repos, res, err := cli.Repositories.ListByOrg(ctx, org, &opts)
		if err != nil {
			return err
		}

		for _, r := range repos {
			if !r.GetArchived() && r.GetPermissions()["push"] {
				fn(r)
			}
		}

		if res.NextPage == 0 {
			return nil
		}

		opts.Page = res.NextPage
	}
}

// forEachRepo calls fn(pr) for each open Dependabot PR in the given repo.
func forEachPR(
	ctx context.Context,
	cli *github.Client,
	r *github.Repository,
	fn func(*github.PullRequest),
) error {
	opts := github.PullRequestListOptions{
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
		Base: r.GetDefaultBranch(),
	}

	for {
		pullRequests, res, err := cli.PullRequests.List(
			ctx,
			r.GetOwner().GetLogin(),
			r.GetName(),
			&opts,
		)
		if err != nil {
			return err
		}

		for _, pr := range pullRequests {
			if pr.GetUser().GetID() == dependabotUserID {
				fn(pr)
			}
		}

		if res.NextPage == 0 {
			return nil
		}

		opts.Page = res.NextPage
	}
}

// merge instructs Dependabot to merge a PR once CI is complete.
func merge(
	ctx context.Context,
	cli *github.Client,
	pr *github.PullRequest,
) error {
	r := pr.GetBase().GetRepo()

	body := "@dependabot merge"

	_, _, err := cli.Issues.CreateComment(
		ctx,
		r.GetOwner().GetLogin(),
		r.GetName(),
		pr.GetNumber(),
		&github.IssueComment{
			Body: &body,
		},
	)

	return err
}

// confirm prompts the user to confirm and returns their choice.
func confirm(f string, args ...interface{}) bool {
	for {
		fmt.Printf(f+" [y/n]: ", args...)
		text, _ := stdin.ReadString('\n')

		switch strings.TrimSpace(strings.ToLower(text)) {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
	}
}
