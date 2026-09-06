# Unix executable capture fixture

`48b409d7d20dafa4e42174d93331b99d11696cc06ffc9d7bdec60d6db03c9b5e.capture`
was generated with `artifact.Store.Build` on Unix. Its key input is
`Unix executable fixture v1`. It contains one file, `executable`, with bytes
`#!/bin/sh\necho fixture\n`, mode `0700`, and mtime Unix second 1. The metadata
contains an empty base reference. The archive checksum is
`aa2a54080185efb3e42ad81f096b8f637fbfc1dd030d5c02bc8409196372e963`.

The Windows test consumes this actual Unix archive and checks for Git mode
`100755`, even though the extracted host file cannot carry Unix execute bits.
