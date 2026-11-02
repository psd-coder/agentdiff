package agent

type Provider string

const (
	Claude   Provider = "claude"
	Codex    Provider = "codex"
	OpenCode Provider = "opencode"
)

// spec is what AgentDiff knows about the CLI of one provider.
type spec struct {
	id   Provider
	name string
	// efforts are the effort levels the CLI takes.
	efforts []string
	args    func(Config, Request) []string
	// stdin is the text the CLI reads on its standard input.
	stdin func(Request) string
	// env are the variables the CLI gets on top of the login shell's, nil
	// for none.
	env func(Config, Request) []string
	// sandbox runs the CLI in a macOS sandbox that refuses writes in the
	// repository, for a CLI whose own rules can miss a write.
	sandbox bool
	parser  func(dir string) lineParser
	// resumed reads the resumed session from the words of a logged
	// command.
	resumed  func(words []string) string
	logItems func(raw []byte) []LogItem
	// withoutSetup says what a run leaves out when the user's own setup
	// is off.
	withoutSetup string
	// modelExample and modelHelp show how the CLI names a model, empty
	// when the CLI takes the plain name of a model.
	modelExample string
	modelHelp    string
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
	{
		id:           OpenCode,
		name:         "OpenCode",
		efforts:      []string{"minimal", "low", "medium", "high", "xhigh", "max"},
		args:         opencodeArgs,
		stdin:        opencodePrompt,
		env:          opencodeEnv,
		sandbox:      true,
		parser:       func(dir string) lineParser { return &opencodeParser{dir: dir} },
		resumed:      opencodeResumed,
		logItems:     opencodeItems,
		modelExample: "provider/model, as opencode/glm-5.3-flash-free",
		modelHelp:    "OpenCode takes the provider and the model. Run opencode models to list the models of your providers.",
		withoutSetup: "OpenCode runs without your opencode.json files, plugins, MCP servers, skills and AGENTS.md files, and without the Claude Code files it reads otherwise. A provider that only your opencode.json sets up is missing then. A key from opencode auth login or from an environment variable still works.",
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

// ModelExample is an example of a model name, empty when the CLI takes the
// plain name of a model.
func (provider Provider) ModelExample() string { return specOf(provider).modelExample }

// ModelHelp says how the CLI names a model, empty when the CLI takes the
// plain name of a model.
func (provider Provider) ModelHelp() string { return specOf(provider).modelHelp }

// WithoutUserSetup says what a run of the provider leaves out when the
// user's own setup is off.
func (provider Provider) WithoutUserSetup() string { return specOf(provider).withoutSetup }

// Sandboxed says whether the CLI runs in the macOS sandbox on macOS,
// which keeps it from writing to the repository. Other systems run the
// CLI without a sandbox.
func (provider Provider) Sandboxed() bool { return specOf(provider).sandbox }
