# Releasing

Before every tagged release:

1. [ ] `scripts/acceptance-windows.ps1` passes on a real Windows machine.
   GitHub's Windows runners can't run Linux containers, so this is manual.
2. [ ] The tag build sets `-ldflags` `-X` values for `main.version`,
   `main.commit`, and `main.buildDate`, and `tamp version` prints all three.
3. [ ] The E2E workflow is green on the tag commit.
