# Custom domain resources without runtime changes

This example defines an unknown `field-notes` domain entirely through an Agent, Skill, Workflow and Kit. No core domain enumeration or business-output repair branch is needed. It reuses the existing file MCP tools.

Copy the example's resource files into the matching `configs/agents/`, `skills/`, `workflows/` and `kits/` directories of a test runtime home. Configure the existing `primary` provider and `file_tools` connection separately; do not copy real credentials. Reload through the resource editor or restart after the initial file-based installation.

The versioned routing contract matches `field notes review` and its Chinese equivalent. Explicit user selections must not be overridden. Missing dependencies must fail preflight rather than silently selecting a software agent.

Provide an input text file, then request: `field notes review: read input/notes.txt and write deliverables/notes.json with source references, observations and unknown fields. Preserve the input and do not invent dates or units.`

Accept only a real parseable output file, unchanged source hash, successful read/write/readback tool evidence and honest quality status. A deterministic local provider proves extension mechanics, not real-model content quality. New execution capabilities may require an external MCP adapter; YAML does not create them.
