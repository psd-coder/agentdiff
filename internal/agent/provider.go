package agent

type Provider string

const (
	Claude Provider = "claude"
	Codex  Provider = "codex"
)

// spec is what AgentDiff knows about the CLI of one provider.
type spec struct {
	id   Provider
	name string
	// efforts are the effort levels the CLI takes.
	efforts []string
	args    func(Config, Request) []string
	// stdin is the text the CLI reads on its standard input.
	stdin  func(Request) string
	parser func(dir string) lineParser
	// resumed reads the resumed session from the words of a logged
	// command.
	resumed  func(words []string) string
	logItems func(raw []byte) []LogItem
	// withoutSetup says what a run leaves out when the user's own setup
	// is off.
	withoutSetup string
}

// specs are the providers, in the order the settings show them.
var specs = []spec{
	{
		id:           Claude,
		name:         "Claude Code",
		efforts:      []string{"low", "medium", "high", "xhigh", "max"},
		args:         claudeArgs,
		stdin:        claudePrompt,
		parser:       func(dir string) lineParser { return &claudeParser{dir: dir} },
		resumed:      claudeResumed,
		logItems:     claudeItems,
		withoutSetup: "Claude Code runs in safe mode: without your CLAUDE.md files, skills, plugins, hooks and MCP servers.",
	},
	{
		id:           Codex,
		name:         "Codex",
		efforts:      []string{"minimal", "low", "medium", "high", "xhigh"},
		args:         codexArgs,
		stdin:        codexPrompt,
		parser:       func(string) lineParser { return &codexParser{} },
		resumed:      codexResumed,
		logItems:     codexItems,
		withoutSetup: "Codex runs without your config.toml, hooks, plugins, apps and the AGENTS.md files of the repository, so a model set in config.toml does not apply. Codex still reads ~/.codex/AGENTS.md and your skills, because the CLI has no option to leave them out.",
	},
}

func lookup(provider Provider) (spec, bool) {
	for _, sp := range specs {
		if sp.id == provider {
			return sp, true
		}
	}
	return spec{}, false
}

// specOf is the spec of p. An unknown provider, as the empty one of an old
// review, reads as Claude, the default provider.
func specOf(provider Provider) spec {
	if sp, ok := lookup(provider); ok {
		return sp
	}
	return specs[0]
}

// Providers lists the providers, in the order the settings show them.
func Providers() []Provider {
	ids := make([]Provider, len(specs))
	for i, sp := range specs {
		ids[i] = sp.id
	}
	return ids
}

// Name is the name of the provider's CLI, as the app shows it.
func (provider Provider) Name() string { return specOf(provider).name }

// Efforts lists the effort levels the CLI of the provider takes.
func (provider Provider) Efforts() []string { return specOf(provider).efforts }

// WithoutUserSetup says what a run of the provider leaves out when the
// user's own setup is off.
func (provider Provider) WithoutUserSetup() string { return specOf(provider).withoutSetup }
