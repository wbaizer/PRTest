package search

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/PullRequestInc/difftools/pkg/models"
	"github.com/PullRequestInc/difftools/pkg/runner"
	"github.com/PullRequestInc/difftools/pkg/utils"
)

// Find text bookended by ANSI control sequences. git-grep highlights matches
// red by default, but the control sequence spec allows several to express the
// color red. Rather than try and parse the actual syntax, we just look for any
// ANSI control sequence.
var matchRE = regexp.MustCompile("(\u001b\\[.*?m)(.*?)(\u001b\\[.*?m)")

// snippetLineLengthMax is the maximum number of characters allowed per line in
// snippet lines. Matches that are after the line limit are not returned.
const snippetLineLengthMax = 300

// NewOptions constructs a new Options with sensible defaults.
func NewOptions() Options {
	var opt Options
	opt.CaseSensitive = true
	opt.Regex = false
	opt.Limit = 100
	opt.Git = runner.NewGit()
	opt.GrepFiles = grepFiles
	opt.SearchPath = true
	opt.SearchContent = true
	return opt
}

// Options represents optional settings for search functions, including
// injection hooks for testing.
type Options struct {
	// If specified, the search is restricted to files changed between BaseSha
	// and the specified revision.
	BaseSha string

	// True if the search is case sensitive.
	CaseSensitive bool

	// Number of surrounding context lines to return for each match.
	ContextLines int

	// Maximum number of matches.
	Limit int

	// Exempts files from the search if false is returned. Aborts the search if
	// an error is returned.
	FileFilter func(path string) (bool, error)

	// True if the search query should be treated as a regex.
	Regex bool

	// Search for query in contents in files
	SearchContent bool

	// Search for query in names of files
	SearchPath bool

	// Overrides for testing.
	Git       runner.Git
	GrepFiles func(ctx context.Context, repoDir string, files []string, query string, caseSensitive bool, regex bool, contextLines int, parser grepParser) ([]*models.SearchResult, int, error)
}

// QuerySearch finds occurrences of 'query' in 'repoDir' content and/or path at 'sha' depending on type
func QuerySearch(ctx context.Context, repoDir, sha, query string, opt Options) ([]*models.SearchResult, int, error) {
	var results []*models.SearchResult
	var unfilteredFiles []string
	var err error
	numMatches := 0
	if opt.BaseSha == "" {
		unfilteredFiles, err = opt.Git.ListFiles(ctx, repoDir, sha)
	} else {
		unfilteredFiles, err = opt.Git.ListChangedFiles(ctx, repoDir, sha, opt.BaseSha)
	}

	if err != nil
{
		return nil, 0, err
	}

	// Checkout the revision in question so we can apply checks below.
	if _, err := opt.Git.Run(ctx, repoDir, "checkout", sha); err != nil {
		return nil, 0, fmt.Errorf("failed to checkout sha %q: %w", sha, err)
	}

	var filteredFiles []string
	for _, path := range unfilteredFiles {
		// Skip symlinks.
		resolvedPath := filepath.Join(repoDir, path)
		if info, err := os.Lstat(resolvedPath); err != nil {
			return nil, 0, fmt.Errorf("failed to stat file %q: %v", resolvedPath, err)
		} else if utils.IsFileLink(info) {
			continue
		}

		// Skip paths equal to "--". git-grep's output is ambiguous if we allow
		// files with this name.
		// TODO: we can make this unambiguous with git-grep's --break flag, however
		// if xargs decides to invoke git-grep multiple times the --break separator
		// may not be emitted between all paths. We could manage the multiple
		// invocations ourselves to avoid this restriction.
		if path == "--" {
			continue
		}

		// Apply user filter, if any.
		if opt.FileFilter != nil {
			if allow, err := opt.FileFilter(path); err != nil {
				return nil, 0, fmt.Errorf("failed to evaluate file %q: %v", resolvedPath, err)
			} else if !allow {
				continue
			}
		}

		filteredFiles = append(filteredFiles, path)
	}

	// Search through filenames for matches
	if opt.SearchPath {
		var queryRE *regexp.Regexp
		if opt.Regex {
			queryRE, err = regexp.Compile(query)
			if err != nil {
				return nil, 0, fmt.Errorf("failed to query path names due to regexp issue: %v", err)
			}
		}
		pathResults, err := findPathMatches(filteredFiles, query, opt.CaseSensitive, queryRE, opt.Limit)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to query path names in dir %q: %v", repoDir, err)
		}
		results = append(results, pathResults...)
		numMatches = numMatches + len(pathResults)
	}

	// Search through contents for matches
	if opt.SearchContent && numMatches < opt.Limit
  
  {
		parser := newGrepParser(opt.Limit - numMatches)
		lineResults, numMatchLines, err := opt.GrepFiles(ctx, repoDir, filteredFiles, query, opt.CaseSensitive, opt.Regex, opt.ContextLines, parser)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to execute search in dir %q: %v", repoDir, err)
		}
		results = append(results, lineResults...)
		numMatches = numMatches + numMatchLines
	}

	 return results, numMatches, nil
  
}
