package share

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/MakeNowJust/heredoc"
	"github.com/atotto/clipboard"
	"github.com/cli/cli/v2/internal/gh"
	"github.com/cli/cli/v2/pkg/cmd/pr/shared"
	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/cli/cli/v2/pkg/iostreams"
	ghTemplate "github.com/cli/go-gh/v2/pkg/template"
	"github.com/spf13/cobra"
)

// defaultTemplate renders a plain-markdown summary suitable for pasting into
// Slack, GitHub comments, or other markdown-aware clients. It intentionally
// avoids GitHub-specific emoji shortcodes (e.g. ":octocat:") since those only
// render correctly on github.com and clients with custom emoji configured.
const defaultTemplate = `**[{{.title}}]({{.url}})** (+{{.additions}}/-{{.deletions}})
{{.headRepositoryOwner.login}}/{{.headRepository.name}}`

// configTemplateKey is the gh config key used to persist a default
// --template value across invocations. It is registered as a known
// configuration key in internal/config.
const configTemplateKey = "pr_share_template"

// shareFields lists the GraphQL fields fetched for the pull request, which
// are also the fields made available to a custom --template.
var shareFields = []string{
	"number", "title", "url", "state", "author",
	"additions", "deletions", "headRepositoryOwner", "headRepository",
}

type ShareOptions struct {
	IO     *iostreams.IOStreams
	Config func() (gh.Config, error)
	Finder shared.PRFinder

	// WriteToClipboard copies the rendered output to the system clipboard.
	// It defaults to clipboard.WriteAll but can be overridden in tests.
	WriteToClipboard func(string) error

	SelectorArg string
	Format      string
	Template    string
	Clipboard   bool
}

func NewCmdShare(f *cmdutil.Factory, runF func(*ShareOptions) error) *cobra.Command {
	opts := &ShareOptions{
		IO:               f.IOStreams,
		Config:           f.Config,
		WriteToClipboard: clipboard.WriteAll,
		Clipboard:        true,
	}

	cmd := &cobra.Command{
		Use:   "share [<number> | <url> | <branch>]",
		Short: "Generate a shareable summary of a pull request",
		Long: heredoc.Docf(`
			Generate a short markdown (or plain URL) summary of a pull request, suitable
			for pasting into Slack, a chat client, or any other markdown-aware tool.

			Without an argument, the pull request that belongs to the current branch is used.

			By default, the summary is printed to standard output and copied to the
			system clipboard. Use %[1]s--clipboard=false%[1]s to skip the clipboard copy.

			The markdown layout can be customized with %[1]s--template%[1]s, which accepts a
			Go template in the same format as %[1]sgh pr view --template%[1]s. A persistent
			default template can be configured with %[1]sgh config set pr_share_template%[1]s.
		`, "`"),
		Example: heredoc.Doc(`
			# copy a markdown summary of the PR for the current branch
			$ gh pr share

			# print the PR URL only, without copying to the clipboard
			$ gh pr share 123 --format url --clipboard=false

			# use a custom template
			$ gh pr share --template '{{.title}} ({{.url}})'
		`),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.Format != "markdown" && opts.Format != "url" {
				return cmdutil.FlagErrorf("invalid value for --format: %q. Valid values are: markdown, url", opts.Format)
			}
			if opts.Format == "url" && opts.Template != "" {
				return cmdutil.FlagErrorf("specify only one of --format=url or --template")
			}

			opts.Finder = shared.NewFinder(f)

			if repoOverride, _ := cmd.Flags().GetString("repo"); repoOverride != "" && len(args) == 0 {
				return cmdutil.FlagErrorf("argument required when using the --repo flag")
			}

			if len(args) > 0 {
				opts.SelectorArg = args[0]
			}

			if runF != nil {
				return runF(opts)
			}
			return shareRun(opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Format, "format", "f", "markdown", "Output `format`: {markdown|url}")
	cmd.Flags().StringVarP(&opts.Template, "template", "t", "", "Format the summary using a Go `template`; see \"gh help formatting\"")
	cmd.Flags().BoolVarP(&opts.Clipboard, "clipboard", "c", true, "Copy the summary to the clipboard")

	return cmd
}

func shareRun(opts *ShareOptions) error {
	pr, _, err := opts.Finder.Find(shared.FindOptions{
		Selector: opts.SelectorArg,
		Fields:   shareFields,
	})
	if err != nil {
		return err
	}

	var output string
	if opts.Format == "url" {
		output = pr.URL
	} else {
		tmpl := opts.Template
		if tmpl == "" {
			tmpl, err = opts.configuredTemplate()
			if err != nil {
				return err
			}
		}
		if tmpl == "" {
			tmpl = defaultTemplate
		}

		output, err = renderTemplate(opts.IO, tmpl, pr.ExportData(shareFields))
		if err != nil {
			return fmt.Errorf("failed to render template: %w", err)
		}
	}

	fmt.Fprintln(opts.IO.Out, output)

	if opts.Clipboard {
		cs := opts.IO.ColorScheme()
		if err := opts.WriteToClipboard(output); err != nil {
			fmt.Fprintf(opts.IO.ErrOut, "%s Failed to copy to clipboard: %s\n", cs.Red("!"), err)
		} else if opts.IO.IsStdoutTTY() {
			fmt.Fprintf(opts.IO.ErrOut, "%s Copied to clipboard\n", cs.SuccessIcon())
		}
	}

	return nil
}

// configuredTemplate returns the user's persistent default template, if any,
// configured via `gh config set pr_share_template '...'`.
func (opts *ShareOptions) configuredTemplate() (string, error) {
	cfg, err := opts.Config()
	if err != nil {
		return "", err
	}
	return cfg.GetOrDefault("", configTemplateKey).Unwrap().Value, nil
}

func renderTemplate(io *iostreams.IOStreams, tmpl string, data map[string]any) (string, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	buf := bytes.Buffer{}
	t := ghTemplate.New(&buf, io.TerminalWidth(), io.ColorEnabled())
	if err := t.Parse(tmpl); err != nil {
		return "", err
	}
	if err := t.Execute(bytes.NewReader(jsonData)); err != nil {
		return "", err
	}
	if err := t.Flush(); err != nil {
		return "", err
	}

	return buf.String(), nil
}
