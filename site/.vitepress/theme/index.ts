import DefaultTheme from 'vitepress/theme'
import type { Theme } from 'vitepress'
import './custom.css'

// RigSmith docs theme — VitePress default theme + the brand design system.
// Tokens, type, and per-tool accents live in custom.css.
export default {
  extends: DefaultTheme,
} satisfies Theme
