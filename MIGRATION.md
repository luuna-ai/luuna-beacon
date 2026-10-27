# Repository extraction, October 21, 2026

At extraction time, the maintainer was brodynelly and the initial destination was https://github.com/brodynelly/luuna-beacon. That destination is historical; the current canonical repository is https://github.com/luuna-ai/luuna-beacon.

- Source: brodynelly/luuna at `fd42d59c5f608b9120d9cdc2e2354b66f945a5ed`, path `components/beacon`.
- Component-scoped history: `git subtree split --prefix=components/beacon fd42d59c5f608b9120d9cdc2e2354b66f945a5ed` produced `bc87c5fe5fca8d43d32eb656ed233df36a20af8a`.
- The extracted baseline tree exactly matches the source subtree before independent-repository metadata changes.
- Original upstream provenance and pre-branding import revision remain in UPSTREAM.md; the complete earlier monorepo history stays in Luuna.
- Source, data formats, aliases, dependencies and lockfile are unchanged by extraction. LICENSE SHA-256: `fe1956a36381a8ac9e13e229247eb1f05f951bb6a96153773025d03a0fd62519`.
- This commit adds standalone ownership, documentation and CI. Luuna consumes the exact reviewed Git commit as a submodule at the existing path.
- No registry package is published. GitHub access supplies source retrieval; npm release credentials are intentionally unnecessary.

Visibility: public, explicitly authorized by the owner on October 21, 2026. Luuna remains private. No private authoring records or credential files are included in the extracted history.

The owner requested the explicit luuna-* repository names on October 21, 2026. GitHub repository identity, history and CI are preserved by rename; the original canonical destination was brodynelly/luuna-beacon; the current canonical destination is luuna-ai/luuna-beacon.

# Project layout move, October 22, 2026

Project data moved from the legacy project-root directory to `.luuna/beacon/`. File names and record formats are unchanged.

- Automatic: any command that needs the store renames a legacy directory to `.luuna/beacon/` when the new one is absent (`.luuna/` is created if needed) and rewrites the `.gitattributes`/`.gitignore` lines this tool previously wrote. One notice line is printed to stderr; stdout (JSON) is unaffected.
- If both the legacy and new directories exist, the command fails with an error asking you to merge or remove the legacy one. Symlinked `.luuna` or legacy directories are refused.
- The `cn` bin alias was removed; use `luuna-beacon` or `lb`.
- Onboard markers changed to `luuna-beacon:start/end`; `lb onboard` upgrades existing sections.
- All legacy names are confined to `src/legacy-layout.ts`.
- Prompt-record fields `mulch`/`extends_mulch` are now `atlas`/`extends_atlas`, and the `render --json` envelope key follows. Old-key records are normalised on read and rewritten with the new keys only when a new version is appended. The alias logic lives in `src/legacy-layout.ts`.
