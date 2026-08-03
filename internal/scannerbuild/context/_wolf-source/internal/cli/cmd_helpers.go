package cli

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// apiAnnotationKey marks, in a command's cobra Annotations, which API endpoint
// the command targets ("<METHOD> <normalized-path>"). TestCLICoversEveryEndpoint
// walks the command tree reading these and asserts the CLI covers every
// non-UI-only endpoint in the OpenAPI catalog.
const apiAnnotationKey = "api"

var (
	cliFmtVerb = regexp.MustCompile(`%[sdvq]`)
	cliBrace   = regexp.MustCompile(`\{[^}]+\}`)
)

// normCLIPath normalizes a CLI path template to the comparison form used by the
// coverage test: query stripped, every fmt verb / {param} collapsed to "{}".
func normCLIPath(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	p = cliFmtVerb.ReplaceAllString(p, "{}")
	p = cliBrace.ReplaceAllString(p, "{}")
	return p
}

// apiAnno builds the cobra Annotations recording the API endpoint a command
// targets. Used by every helper below and by inline commands.
func apiAnno(method, pathTemplate string) map[string]string {
	return map[string]string{apiAnnotationKey: strings.ToUpper(method) + " " + normCLIPath(pathTemplate)}
}

// group builds a parent command with no action of its own.
func group(use, short string, subs ...*cobra.Command) *cobra.Command {
	g := &cobra.Command{Use: use, Short: short}
	g.AddCommand(subs...)
	return g
}

// listCmd builds a "list" subcommand that GETs a collection path, with
// optional --page / --per-page pagination.
func listCmd(path, short string) *cobra.Command {
	var page, perPage int
	c := &cobra.Command{
		Use:         "list",
		Short:       short,
		Args:        cobra.NoArgs,
		Annotations: apiAnno("GET", path),
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := path
			q := url.Values{}
			if cmd.Flags().Changed("page") {
				q.Set("page", strconv.Itoa(page))
			}
			if cmd.Flags().Changed("per-page") {
				q.Set("per_page", strconv.Itoa(perPage))
			}
			if len(q) > 0 {
				p += "?" + q.Encode()
			}
			return runRender(cmd, "GET", p, nil)
		},
	}
	c.Flags().IntVar(&page, "page", 1, "page number")
	c.Flags().IntVar(&perPage, "per-page", 25, "results per page")
	return c
}

// getCmd builds a "get <id>" subcommand that GETs path/<id>.
func getCmd(path, short string) *cobra.Command {
	return &cobra.Command{
		Use:         "get <id>",
		Short:       short,
		Args:        cobra.ExactArgs(1),
		Annotations: apiAnno("GET", path+"/{}"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, "GET", path+"/"+args[0], nil)
		},
	}
}

// deleteCmd builds a "<use> <id>" subcommand that DELETEs a formatted path.
func deleteCmd(use, short, pathFmt string) *cobra.Command {
	return &cobra.Command{
		Use:         use,
		Short:       short,
		Args:        cobra.ExactArgs(1),
		Annotations: apiAnno("DELETE", pathFmt),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, "DELETE", fmt.Sprintf(pathFmt, args[0]), nil)
		},
	}
}

// subGetCmd builds a "<verb> <id>" subcommand GETting a formatted sub-path.
func subGetCmd(use, short, pathFmt string) *cobra.Command {
	return &cobra.Command{
		Use:         use,
		Short:       short,
		Args:        cobra.ExactArgs(1),
		Annotations: apiAnno("GET", pathFmt),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, "GET", fmt.Sprintf(pathFmt, args[0]), nil)
		},
	}
}

// watchCmd builds a "watch <id>" subcommand that streams an SSE endpoint.
func watchCmd(short, pathFmt string) *cobra.Command {
	return &cobra.Command{
		Use:         "watch <id>",
		Short:       short,
		Args:        cobra.ExactArgs(1),
		Annotations: apiAnno("GET", pathFmt),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			return c.Stream(cmd.Context(), fmt.Sprintf(pathFmt, args[0]), func(line string) {
				fmt.Fprintln(cmd.OutOrStdout(), line)
			})
		},
	}
}

// actionCmd builds a "<use> <id>" subcommand issuing a no-body request.
func actionCmd(use, short, method, pathFmt string) *cobra.Command {
	return &cobra.Command{
		Use:         use,
		Short:       short,
		Args:        cobra.ExactArgs(1),
		Annotations: apiAnno(method, pathFmt),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, method, fmt.Sprintf(pathFmt, args[0]), nil)
		},
	}
}
