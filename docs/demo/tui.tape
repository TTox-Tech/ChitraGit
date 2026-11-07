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
Set Theme { "name": "Ledger", "black": "#0A0C10", "red": "#F47067", "green": "#72C795", "yellow": "#E3B341", "blue": "#7DD3FC", "magenta": "#3B82F6", "cyan": "#72C795", "white": "#E6EDF3", "brightBlack": "#8B949E", "brightRed": "#F47067", "brightGreen": "#72C795", "brightYellow": "#E3B341", "brightBlue": "#7DD3FC", "brightMagenta": "#3B82F6", "brightCyan": "#72C795", "brightWhite": "#E6EDF3", "background": "#0A0C10", "foreground": "#E6EDF3", "selection": "#2B313A", "cursor": "#3B82F6" }

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
