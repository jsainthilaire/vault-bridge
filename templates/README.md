# Vault templates

These Go text templates are embedded in the Vault Bridge executable. They work
from any working directory without needing a copy of this source checkout.
Rebuild with `make build` after editing a `.tmpl` file.

Create a vault and project in an empty or missing directory:

```sh
./.bin/vault-bridge vault init --path /absolute/path/to/MyVault \
  --id my-project --title "My Project"
```

The command creates the standard vault layout, project planning notes in
`03-Projects/my-project/`, and rendered Markdown copies of these templates in
`08-Templates/`. Existing notes and templates are preserved when initialization
runs again; template changes apply to newly created files.

| Source | Purpose |
| --- | --- |
| `Project.md.tmpl` | Project overview, metadata and links to planning notes |
| `Vision.md.tmpl` | Problem, goals, design principles and non-goals |
| `Requirements.md.tmpl` | Required behavior, constraints and acceptance evidence |
| `Architecture.md.tmpl` | System boundary, interfaces and decision links |
| `Roadmap.md.tmpl` | Milestones, dependencies and risks |
| `Tasks.md.tmpl` | Next actions and verification |
| `Feature.md.tmpl` | Feature requirements and acceptance criteria |
| `ADR.md.tmpl` | Decision context, alternatives and consequences |
| `Research.md.tmpl` | Findings, sources and implications |
| `Session.md.tmpl` | Work performed, actual check results and next steps |
| `Idea.md.tmpl` | Opportunity, hypothesis and next action |
| `Home.md.tmpl` | Vault dashboard |
| `note.md.tmpl` | Wrapper for Vault Bridge's built-in planning content |

The renderer supplies `.ID`, `.Title`, `.Type`, `.Date` and `.Body` where needed.
`quote` safely quotes YAML strings. Dates use UTC `YYYY-MM-DD` at initialization.
Project planning notes receive the selected project ID; reusable copies use
`project-id` as a placeholder. When copying a reusable template into a note,
replace placeholder IDs, titles, dates and links with that note's values.

Templates contain planning prompts. Checkboxes and session notes should record
completed work or successful checks only after there is evidence for them.
