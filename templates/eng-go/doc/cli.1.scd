EXAMPLE_NAME(1)

# NAME

EXAMPLE_NAME - print the program's version

# SYNOPSIS

*EXAMPLE_NAME*

# DESCRIPTION

*EXAMPLE_NAME* prints one line, "_name_ _version_+_commit_", and exits. It is
the scaffold's placeholder; replace this page as the program grows.

The page lives in _doc/cli.1.scd_ and is built into the *manpages* package by
scdoc, installed with *packages.default* under _share/man/man1/_. The file name
is fixed; the installed page is named after the package. Add further pages as
_doc/\*.N.scd_ for section N.

# NAME LINE

The first line under NAME is "_name_ - _summary_", on one line, short enough to
render untruncated in a manpage index. *doppelgang lint-man* checks every
installed page against that contract (_just lint-manpage-names_).

# EXIT STATUS

*0* on success, *1* if the line cannot be written.
