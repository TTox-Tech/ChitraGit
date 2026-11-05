# Records docs/assets/demo-tui.gif: the interactive ledger browsing and syncing the demo archive.
# Run from the repo root after docs/demo/make-archive.sh /tmp/chitragit-demo:
#   vhs docs/demo/tui.tape
Output docs/assets/demo-tui.gif

Set Shell "bash"
Set FontSize 16
Set Width 1400
Set Height 800
Set Padding 16
Set TypingSpeed 80ms
Set Theme { "name": "Ledger", "black": "#16120E", "red": "#E07A66", "green": "#8DB58A", "yellow": "#DDA654", "blue": "#CDB27A", "magenta": "#E58A45", "cyan": "#8DB58A", "white": "#EFE6D6", "brightBlack": "#A39684", "brightRed": "#E07A66", "brightGreen": "#8DB58A", "brightYellow": "#DDA654", "brightBlue": "#CDB27A", "brightMagenta": "#E58A45", "brightCyan": "#8DB58A", "brightWhite": "#EFE6D6", "background": "#16120E", "foreground": "#EFE6D6", "selection": "#3D342A", "cursor": "#E58A45" }

Hide
Type "cd /tmp/chitragit-demo/archive && export PS1='$ ' && clear"
Enter
Show

Type "chitragit --no-wizard"
Enter
Sleep 3s

# Browse and search the repo list.
Down 3
Sleep 600ms
Type "/"
Sleep 300ms
Type "char"
Sleep 1s
Enter
Sleep 600ms
Escape
Sleep 600ms

# Open one repo's details, then go back.
Enter
Sleep 2.5s
Escape
Sleep 800ms

# Sync everything and watch the ledger fill.
Type "S"
Sleep 6s

# Tour the other screens.
Tab
Sleep 2s
Tab
Sleep 2s
Tab
Sleep 2s
Type "q"
Sleep 1s
