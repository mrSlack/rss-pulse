# Code Formatting in Your IDE

Shared settings live in the root `.editorconfig`: UTF-8, LF line endings,
a final newline, and removal of trailing whitespace. Markdown retains trailing
spaces because they can represent line breaks.

- **Backend:** standard `gofmt`, with tab indentation displayed at a width of 8.
- **Frontend:** `frontend/.prettierrc.json`, with 2-space indentation, single
  quotes in JS/TS, double quotes in JSX, semicolons, trailing commas, and a target
  line width of 120 characters. Generated files are excluded through
  `frontend/.prettierignore`.

## VS Code

Open the repository root and install the extensions listed under Workspace
Recommendations: EditorConfig, Go, and Prettier. The settings in
`.vscode/settings.json` enable formatting on save for Go and frontend files.
Prettier runs only where its configuration is found. The Prettier extension can
use its bundled version; a separate npm dependency is not required for
formatting in VS Code.

Go requires an installed Go toolchain and the Go extension's tools.
Having Go available only in Docker does not enable formatting in a local IDE.

## GoLand / WebStorm / IntelliJ IDEA

Enable EditorConfig support in the Code Style settings. For Go, use the `gofmt`
action or configure it to run on save. For frontend files, enable Prettier
integration with formatting on save and select an installed Prettier package.
Configuration is read from `frontend/.prettierrc.json`. Avoid enabling both
built-in Reformat Code and Prettier for frontend files so they do not reformat
each other's output.

These settings define rules for future edits; existing source files have not
been reformatted in bulk.
