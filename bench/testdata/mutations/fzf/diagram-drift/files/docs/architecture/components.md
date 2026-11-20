# fzf architecture

## fzf components

```mermaid
flowchart LR
  cli --> core
  cli --> protector
  core --> tui
  core --> algo
  core --> util
  tui --> util
  algo --> util
  protector --> util
```
