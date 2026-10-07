package share

import (
	"errors"
	"testing"

	"github.com/cli/cli/v2/api"
	"github.com/cli/cli/v2/internal/gh"
	ghmock "github.com/cli/cli/v2/internal/gh/mock"
	"github.com/cli/cli/v2/internal/ghrepo"
	"github.com/cli/cli/v2/pkg/cmd/pr/shared"
	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/cli/cli/v2/pkg/iostreams"
	o "github.com/cli/cli/v2/pkg/option"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_NewCmdShare(t *testing.T) {
	tests := []struct {
		name    string
		args    string
		isTTY   bool
		want    ShareOptions
		wantErr string
	}{
		{
			name:  "no argument",
			args:  "",
			isTTY: true,
			want: ShareOptions{
				SelectorArg: "",
				Format:      "markdown",
				Clipboard:   true,
			},
		},
		{
			name:  "number argument",
			args:  "123",
			isTTY: true,
			want: ShareOptions{
				SelectorArg: "123",
				Format:      "markdown",
				Clipboard:   true,
			},
		},
		{
			name: "format url",
			args: "123 --format url",
			want: ShareOptions{
				SelectorArg: "123",
				Format:      "url",
				Clipboard:   true,
			},
		},
		{
			name: "clipboard disabled",
			args: "123 --clipboard=false",
			want: ShareOptions{
				SelectorArg: "123",
				Format:      "markdown",
				Clipboard:   false,
			},
		},
		{
			name: "custom template",
			args: "123 --template '{{.title}}'",
			want: ShareOptions{
				SelectorArg: "123",
				Format:      "markdown",
				Template:    "{{.title}}",
				Clipboard:   true,
			},
		},
		{
			name:    "invalid format",
			args:    "123 --format bogus",
			wantErr: "invalid value for --format: \"bogus\". Valid values are: markdown, url",
		},
		{
			name:    "format url with template",
			args:    "123 --format url --template '{{.title}}'",
			wantErr: "specify only one of --format=url or --template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, _, _ := iostreams.Test()
			ios.SetStdoutTTY(tt.isTTY)
			ios.SetStdinTTY(tt.isTTY)
			ios.SetStderrTTY(tt.isTTY)

			f := &cmdutil.Factory{
				IOStreams: ios,
			}

			var opts *ShareOptions
			cmd := NewCmdShare(f, func(o *ShareOptions) error {
				opts = o
				return nil
			})
			cmd.PersistentFlags().StringP("repo", "R", "", "")

			argv, err := shlex.Split(tt.args)
			require.NoError(t, err)
			cmd.SetArgs(argv)

			cmd.SetIn(&testNopCloser{})
			cmd.SetOut(&testNopCloser{})
			cmd.SetErr(&testNopCloser{})

			_, err = cmd.ExecuteC()
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)

			assert.Equal(t, tt.want.SelectorArg, opts.SelectorArg)
			assert.Equal(t, tt.want.Format, opts.Format)
			assert.Equal(t, tt.want.Template, opts.Template)
			assert.Equal(t, tt.want.Clipboard, opts.Clipboard)
		})
	}
}

type testNopCloser struct{}

func (testNopCloser) Read(p []byte) (int, error)  { return 0, nil }
func (testNopCloser) Write(p []byte) (int, error) { return len(p), nil }
func (testNopCloser) Close() error                { return nil }

func Test_shareRun(t *testing.T) {
	pr := &api.PullRequest{
		Number:    123,
		Title:     "Some title",
		URL:       "https://github.com/OWNER/REPO/pull/123",
		Additions: 10,
		Deletions: 5,
	}
	pr.HeadRepositoryOwner.Login = "OWNER"
	pr.HeadRepository = &api.PRRepository{Name: "REPO"}

	tests := []struct {
		name          string
		opts          ShareOptions
		configTmpl    string
		clipboardErr  error
		wantStdout    string
		wantStderr    string
		wantClipboard string
		wantErr       string
	}{
		{
			name: "default markdown template",
			opts: ShareOptions{
				Format:    "markdown",
				Clipboard: true,
			},
			wantStdout:    "**[Some title](https://github.com/OWNER/REPO/pull/123)** (+10/-5)\nOWNER/REPO\n",
			wantClipboard: "**[Some title](https://github.com/OWNER/REPO/pull/123)** (+10/-5)\nOWNER/REPO",
		},
		{
			name: "format url",
			opts: ShareOptions{
				Format:    "url",
				Clipboard: true,
			},
			wantStdout:    "https://github.com/OWNER/REPO/pull/123\n",
			wantClipboard: "https://github.com/OWNER/REPO/pull/123",
		},
		{
			name: "custom template flag",
			opts: ShareOptions{
				Format:    "markdown",
				Template:  "{{.title}} ({{.url}})",
				Clipboard: true,
			},
			wantStdout:    "Some title (https://github.com/OWNER/REPO/pull/123)\n",
			wantClipboard: "Some title (https://github.com/OWNER/REPO/pull/123)",
		},
		{
			name: "configured default template",
			opts: ShareOptions{
				Format:    "markdown",
				Clipboard: true,
			},
			configTmpl:    "{{.title}}",
			wantStdout:    "Some title\n",
			wantClipboard: "Some title",
		},
		{
			name: "clipboard disabled",
			opts: ShareOptions{
				Format:    "markdown",
				Clipboard: false,
			},
			wantStdout:    "**[Some title](https://github.com/OWNER/REPO/pull/123)** (+10/-5)\nOWNER/REPO\n",
			wantClipboard: "",
		},
		{
			name: "clipboard failure is non-fatal",
			opts: ShareOptions{
				Format:    "markdown",
				Clipboard: true,
			},
			clipboardErr: errors.New("no clipboard available"),
			wantStdout:   "**[Some title](https://github.com/OWNER/REPO/pull/123)** (+10/-5)\nOWNER/REPO\n",
			wantStderr:   "! Failed to copy to clipboard: no clipboard available\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, stdout, stderr := iostreams.Test()
			ios.SetStdoutTTY(false)
			ios.SetStdinTTY(false)
			ios.SetStderrTTY(false)

			tt.opts.IO = ios
			tt.opts.Finder = shared.NewMockFinder("", pr, ghrepo.New("OWNER", "REPO"))
			tt.opts.Config = func() (gh.Config, error) {
				return &ghmock.ConfigMock{
					GetOrDefaultFunc: func(hostname, key string) o.Option[gh.ConfigEntry] {
						return o.Some(gh.ConfigEntry{Value: tt.configTmpl})
					},
				}, nil
			}

			var clipboardContent string
			tt.opts.WriteToClipboard = func(s string) error {
				if tt.clipboardErr != nil {
					return tt.clipboardErr
				}
				clipboardContent = s
				return nil
			}

			err := shareRun(&tt.opts)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)

			assert.Equal(t, tt.wantStdout, stdout.String())
			assert.Equal(t, tt.wantStderr, stderr.String())
			assert.Equal(t, tt.wantClipboard, clipboardContent)
		})
	}
}
