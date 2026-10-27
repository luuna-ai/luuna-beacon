# Upstream provenance

- Original project: Canopy
- Original package: @os-eco/canopy-cli
- Upstream repository: https://github.com/jayminwest/canopy
- Imported upstream version: 0.2.6
- Import source: the tracked canopy/ tree in brodynelly/luuna at commit 532048d1839d003d724e55e8a47dbfdc44145004 (see repository history for the full tree).
- Luuna identity: @luuna/beacon-cli, in components/beacon/.
- Upstream copyright holder and author: Jaymin West, as retained in LICENSE and package authorship.

The original MIT LICENSE is preserved verbatim. Luuna modifications are also licensed under MIT by Luuna contributors. Original version numbers remain the compatibility baseline; a distinct Luuna release version must be chosen before publication.

Luuna changes include branding, private package metadata, safe installation without Git-hook mutation, current agent guidance, removal of imported authoring/session records, and compiling the fixed config validator before acquiring the write lock to prevent concurrent writers timing out. Storage formats, historical command aliases, exported type names, environment variables, schema fields, and integration markers retain upstream compatibility. No rewrite of the implementation is implied.

This maintained distribution is currently published at https://github.com/luuna-ai/luuna-beacon. Luuna consumes it as an exact-commit Git submodule. The package remains private and unpublished. The component-scoped history was extracted with git subtree split; the original pre-branding history remains in the original Luuna core repository at the import source recorded above. See MIGRATION.md.

Imported agent instructions and authoring metadata were preserved locally before removal from the working tree. They must not be loaded as Luuna project state. Historical documentation remains reference material; current component development starts with this repository's AGENTS.md. Luuna runtime policy remains in the Luuna repository.
