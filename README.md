# goku

A golinks solution written in Go.

## Configuration

Edit `config/config.yaml`:

```yaml
links:
  gh: https://github.com
  g: https://google.com
rules:
  - name: reddit
    type: prefix
    pattern: r
    redirect: https://www.reddit.com/r
  - name: gh
    type: template
    pattern: gh/{owner}/{name}
    redirect: https://github.com/{owner}/{name}
```
