package search

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"

	"github.com/yllix/stowr/v3/rules"
)

type searchOptions struct {
	CaseSensitive bool
	Conditions    []condition
	Terms         []string
}

// Search searches for a query in a fs.
func Search(ctx context.Context,
	fs afero.Fs, scope, query string, checker rules.Checker, found func(path string, f os.FileInfo) error) error {
	match := Matcher(query)

	scope = filepath.ToSlash(filepath.Clean(scope))
	scope = path.Join("/", scope)

	return afero.Walk(fs, scope, func(fPath string, f os.FileInfo, _ error) error {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		fPath = filepath.ToSlash(filepath.Clean(fPath))
		fPath = path.Join("/", fPath)
		relativePath := strings.TrimPrefix(fPath, scope)
		relativePath = strings.TrimPrefix(relativePath, "/")

		if fPath == scope {
			return nil
		}

		if !checker.Check(fPath) {
			return nil
		}

		if !match(fPath) {
			return nil
		}

		return found(relativePath, f)
	})
}

// Matcher returns the predicate used by Search for query. It receives an
// entry's path and reports whether the entry is a hit: it must match at least
// one type: condition when any are given, and contain at least one search
// term in its name when any are given.
func Matcher(query string) func(fPath string) bool {
	search := parseSearch(query)

	return func(fPath string) bool {
		if len(search.Conditions) > 0 {
			match := false
			for _, t := range search.Conditions {
				if t(fPath) {
					match = true
					break
				}
			}
			if !match {
				return false
			}
		}

		if len(search.Terms) == 0 {
			return true
		}

		_, fileName := path.Split(fPath)
		if !search.CaseSensitive {
			fileName = strings.ToLower(fileName)
		}
		for _, term := range search.Terms {
			if !search.CaseSensitive {
				term = strings.ToLower(term)
			}
			if strings.Contains(fileName, term) {
				return true
			}
		}
		return false
	}
}
