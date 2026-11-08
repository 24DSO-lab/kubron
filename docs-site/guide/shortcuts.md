# Keyboard shortcuts

Kubron has a small set of keyboard shortcuts. This page lists the ones that exist in the current version.

## Jump to a view

On the cluster **Overview** (the dashboard), these shortcuts open the matching view:

| Shortcut | Action |
|---|---|
| `Alt + P` | Jump to Pods |
| `Alt + D` | Jump to Deployments |
| `Alt + N` | Jump to Nodes |
| `Alt + E` | Jump to Events |
| `Alt + S` | Open Settings |

Notes:

- They are registered while the Overview is open. From another view, use the sidebar or return to the Overview first.
- Press `Alt` with the letter only. Combinations that also include `Ctrl`, `Shift` or `Cmd` are ignored.
- They are ignored while you type in a text field, search box or editor, so they never interfere with typing.
- On macOS, `Alt` is the `Option` key.

## Dialogs and menus

| Shortcut | Where | Action |
|---|---|---|
| `Esc` | Dialogs, context menus, the cluster menu, the column picker | Close it. Add and edit cluster dialogs ignore `Esc` while a save is running |
| `Ctrl + Enter` (`Cmd + Enter` on macOS) | Add cluster and edit cluster dialogs | Submit the form |
| `Tab` | Sign-in dialog for a cluster | Moves between its fields; focus stays inside the dialog |

## Resizing panels

The sidebar and the resource detail panel have a draggable divider. With the divider focused, the arrow keys resize it:

| Shortcut | Action |
|---|---|
| `→` / `←` | Sidebar: wider / narrower |
| `←` / `→` | Detail panel (on the right): wider / narrower |
| `Shift` + arrow | The same, with a step four times larger |

Kubron remembers the width you choose.

## YAML editor and terminal

The YAML editor is the Monaco editor and the terminal is xterm, so their own standard shortcuts apply inside them, for example `Ctrl + F` to search in the editor. Their shortcuts are provided by those components and are not specific to Kubron.

More shortcuts will be documented as they are added.
