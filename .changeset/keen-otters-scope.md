---
type: fix
---

The .NET adapter no longer mistakes an `<ItemGroup>` item's `<Version>` metadata for the project's version — it was reading the wrong number, and the matching write path was corrupting the item.

MSBuild spells item metadata with the same element syntax as a property, so a custom item can legitimately carry its own `<Version>`. Avalite's icon packs do exactly that: an `<IconPack>` item declares the pack's version alongside its name and source directory. The adapter matched `<Version>` anywhere in the document, so for a project whose `<ItemGroup>` precedes its `<PropertyGroup>` it reported the icon pack's `0.2.0` as the package version — visible in `shiprig info` as one package sitting at a version nothing had ever released.

The write half was worse, because `writeVersion` targets "the same element `fromText` reads". A bump would have spliced the new version into the icon pack's metadata and left the project's own version untouched — silently changing a value that feeds the baked pack manifest, while the release appeared to succeed. Nothing about that surfaces until someone notices the pack version drifting with each release.

Version reads and writes are now scoped to `<PropertyGroup>` blocks, so item metadata is invisible to both. The element-choice in `SetVersion` (`Version` wins over `VersionPrefix`) is scoped the same way, since an item's metadata should not decide which element a project bumps. A project whose only `<Version>` is item metadata is correctly treated as having no project version, so the bump is inserted into a `PropertyGroup` rather than hijacking the item.

Three tests cover it — discovery, the in-place bump, and the insert-when-absent path — each verified to fail against the previous behaviour.
