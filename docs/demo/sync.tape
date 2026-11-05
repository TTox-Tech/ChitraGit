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
Set Theme { "name": "Ledger", "black": "#16120E", "red": "#E07A66", "green": "#8DB58A", "yellow": "#DDA654", "blue": "#CDB27A", "magenta": "#E58A45", "cyan": "#8DB58A", "white": "#EFE6D6", "brightBlack": "#A39684", "brightRed": "#E07A66", "brightGreen": "#8DB58A", "brightYellow": "#DDA654", "brightBlue": "#CDB27A", "brightMagenta": "#E58A45", "brightCyan": "#8DB58A", "brightWhite": "#EFE6D6", "background": "#16120E", "foreground": "#EFE6D6", "selection": "#3D342A", "cursor": "#E58A45" }

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
