---
"@acme/core": major
"@acme/cli": major
type: feat!
---

feat!: rename the `parse()` entrypoint to `read()`

The old `parse()` export is removed. Update imports:

    - import { parse } from '@acme/core'
    + import { read } from '@acme/core'

`read()` takes the same options object, so no other changes are needed.
