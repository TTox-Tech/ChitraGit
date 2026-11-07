# Records docs/assets/demo-cli.gif: a headless sync of the demo archive.
# Run from the repo root after docs/demo/make-archive.sh /tmp/chitragit-demo:
#   vhs docs/demo/sync.tape
Output docs/assets/demo-cli.gif

Set Shell "bash"
Set FontSize 16
Set Width 1280
Set Height 760
Set Padding 24
Set TypingSpeed 60ms
Set Theme { "name": "Ledger", "black": "#0A0C10", "red": "#F47067", "green": "#72C795", "yellow": "#E3B341", "blue": "#7DD3FC", "magenta": "#3B82F6", "cyan": "#72C795", "white": "#E6EDF3", "brightBlack": "#8B949E", "brightRed": "#F47067", "brightGreen": "#72C795", "brightYellow": "#E3B341", "brightBlue": "#7DD3FC", "brightMagenta": "#3B82F6", "brightCyan": "#72C795", "brightWhite": "#E6EDF3", "background": "#0A0C10", "foreground": "#E6EDF3", "selection": "#2B313A", "cursor": "#3B82F6" }

Hide
Type "cd /tmp/chitragit-demo/archive && export PS1='$ ' && clear"
Enter
Show

Sleep 500ms
Type "chitragit sync"
Sleep 400ms
Enter
Sleep 4s
Type "chitragit status"
Sleep 400ms
Enter
Sleep 4s
