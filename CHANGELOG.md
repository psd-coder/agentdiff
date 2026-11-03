# Changelog

## 0.5.0 - 2026-11-03

### Added

- AgentDiff runs on Windows and Linux. A release has an installer for Windows on x64, and the Linux app for x64 and ARM64 as a `.deb` and as a `.tar.gz` with `install.sh`. The README explains how to install them. On these systems, menus and tooltips show shortcuts as Ctrl+Shift+P, File holds Settings, and the windows keep the title bar of the system. On Windows, the app only runs agent CLIs that are `.exe` files.
- OpenCode is a third agent, next to Claude Code and Codex. With OpenCode, the app works with an API key of Anthropic, OpenAI, OpenRouter and many other providers. The README explains how to set it up. On macOS, OpenCode cannot change the files of the repository, because it runs in a sandbox. On Windows and Linux, only its permission rules keep it read-only.
- The agent can run in a dev container. Set Command in Settings > Agent to the command that runs the CLI there, such as `dev claude`. The README lists what the container needs.
- The code snippets of the walkthroughs are colored with the code theme. You can select their lines and copy them without the line numbers.
- The diff colors Astro, Vue and Svelte components. Their scripts are colored as TypeScript or JavaScript, and so are the expressions in the template.

### Changed

- Each agent in Settings > Agent keeps its own model, effort, command, extra arguments and setup option. A switch to another agent no longer passes the model of one CLI to another one. Your current settings move to the agent you use now. The Binary field is now Command, and it keeps the path you set.

## 0.4.0 - 2026-11-02

### Added

- You can rewrite every prompt the app sends to the agent in Settings > Prompts. A repository can give its own prompts in `.agentdiff/prompts/<name>.md`. A prompt can ask for sections next to the steps, such as a list of risks, and each section item gets its own threads. If you changed the checklist questions, the app moves them into the overview prompt.
- A new setting in Settings > Agent loads your own instructions, skills, hooks and MCP servers into agent runs. It is off by default.
- When a run you started ends while another app is in front, AgentDiff shows a notification. A click on the notification opens the result. The Dock icon shows the number of results you have not seen yet.
- A walkthrough, question or thread whose run ends while you look at another page gets a dot on its row. Its scope gets a dot in the list of changes. A row of a scope shows a spinner while its overview runs.
- Context menus on files and groups in the sidebar, on the Overview and walkthroughs, on commits, on threads and on recent projects.
- File > Open Recent. The Dock menu lists the recent repositories, and you can drop a folder on the Dock icon to open it.
- The window title names the project and the scope. The close button shows a dot while the review has feedback that you did not copy or save yet.
- The Overview of a commit shows the full commit message, its hash, author and date.
- The sidebar lists all commits of a branch or range and reads them 50 at a time as you scroll. A failed page of commits has a Try again link.
- The About panel shows the author and a link to the GitHub repository.
- Tooltips of the sidebar items and buttons show their shortcuts. Context menus show the shortcut of each command.

### Changed

- The window looks more like a Mac app. The toolbar sits in the title bar and shows its actions as icons. Back and Forward are toolbar buttons, and the "Back to" bar above the page is gone. The sidebar is translucent, and its headings are in normal case.
- Settings open in their own window from the app menu or with Cmd+Comma. The Settings button in the toolbar and the Done button are gone, because every change saves at once.
- Without a repository to open, the app starts with a small welcome window that lists the recent repositories.
- Cmd+1 to Cmd+4 now open the pages in the order of the sidebar: Overview, All files diff, Questions & comments and Feedback. A shortcut you changed in the settings stays as it is.
- Agent runs no longer load your own instructions, skills, hooks, plugins and MCP servers by default. Claude Code runs with `--safe-mode`, so your Claude Code version must have this option. Codex runs without your `config.toml`, hooks, plugins, apps and the `AGENTS.md` files of the repository.
- The checklist is now a section that the default overview prompt asks for. The questions setting is gone.
- Regenerate, Log, Start over and Delete on the Overview and walkthrough pages are in a menu next to the page title.
- When a project has no uncommitted changes, it opens all changes of the branch since its base, or the newest commit on a branch without a base.
- Each item keeps one agent session for each provider. When you switch between Claude and Codex, each agent continues its own earlier session.
- A run you stop shows "Stopped" in gray, and no longer shows as a failure.
- Questions and Go deeper start at once, even while the app writes deeper walkthroughs ahead.
- The flat file list shows the folder on a second line under the file name.
- Remove from List in the context menu of a recent project replaces the trash button.

### Fixed

- A review or config file that the app cannot read is kept as a `.bad` file. Before, the next save wrote over it, and all comments and threads were lost.
- A red notice shows when a review or the settings cannot be saved. Start over, delete, move and restore of an earlier review show an error when the store fails.
- A crash when the changes of an earlier review came back to the working tree, for example after `git stash pop`.
- A review deleted with Start over while agents still ran came back on the next launch.
- With `diff.noprefix` or `diff.mnemonicPrefix` in the git config, the uncommitted review went to the archive on every load.
- A committed symlink to a folder outside the repository let the app read files through it.
- The app could freeze on a walkthrough reference with a huge line number.
- A thread could start a second agent run while the first one was still running.
- Errors from git now show git's own message, and the commit and branch lists show git errors in place of an empty list.
- A run that delivered its result showed as stopped and failed in the run log when the CLI was still running its hooks.
- A question row showed "failed" when the first answer failed, even after a follow-up got an answer.
- The "Moved here from" notice did not show when the uncommitted review went to the archive.
- An open group page jumped to another group when the overview finished or the files changed.
- Panels of a review stayed open after you switched scope, so a question could go into the review of another scope.

### Performance

- The diffs of a scope show much sooner. Reading 200 changed files takes about 90 ms instead of 6 to 14 seconds.
- Long overviews, many files and many threads in the sidebar use less time and memory per frame.
- A load stops its git work when a newer load starts.

## 0.3.0 - 2026-10-30

### Performance

- The group and all-files pages build only the file cards on screen.

## 0.2.0 - 2026-10-30

### Added

- The open project refreshes when its files or its git history change.

### Changed

- The file header stays at the top while its diff scrolls.
- Refresh reads the commit list again.

## 0.1.0 - 2026-10-30

First release.
