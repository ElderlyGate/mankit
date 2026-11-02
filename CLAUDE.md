READ THE ORG CONSTITUTION FIRST, in this order, stopping at the first step that works:
1. `hubman path constitution` prints a folder: read it.
2. hubman is installed but answers "not installed": run `hubman install constitution --yes`, then step 1.
3. No hubman: use ~/.local/share/constitution; if it is missing, clone git@github.com:ElderlyGate/constitution.git there; if it exists, `git -C ~/.local/share/constitution pull` to bring it to the latest.

If no step works, or the copy could not be brought to the latest, STOP and tell the user which step failed. Never work from a copy that may be old.
Then read this project's constitution/. A rule there holds unless the project overrides it by ID.

WORK FROM THE CARDS ON THE BOARD. ARCHIVED CARDS ARE IGNORED.
