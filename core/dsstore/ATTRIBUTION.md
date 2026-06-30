# Third-party code

- `dsstore.go`, `reader.go`, `writer.go`: vendored from
  https://github.com/gwend/dsstore (MIT, © 2020 Kenty Gwend), with the
  `golang.org/x/text` UTF-16 dependency replaced by stdlib `unicode/utf16`
  (see `utf16.go`).
- `alias.go`: the Alias Manager v2 serialization is a Go port of
  https://github.com/al45tair/mac_alias `alias.py` (MIT, © 2014 Alastair
  Houghton, © 2022 Russell Keith-Magee).
