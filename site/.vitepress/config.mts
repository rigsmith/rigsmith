import { defineConfig } from 'vitepress'

// The site is keyed by binary name: /rig/, /changerig/, /shiprig/, /clauderig/,
// plus /core/ (the engine) and /compare/. Each tool section gets its own
// sidebar, selected by path prefix below. Brand casing per the family
// convention: `Rig` capitalized in prose (shipRig, changeRig, claudeRig),
// lowercase in commands/paths. See docs/WEBSITE.md + docs/SHIPRIG-RENAME-PLAN.md.

const GITHUB = 'https://github.com/JohnCampionJr/rigsmith'

export default defineConfig({
  title: 'RigSmith',
  description:
    'A family of convention-first, zero-runtime-dependency CLI tools for polyglot monorepos.',
  lang: 'en-US',
  cleanUrls: true,
  lastUpdated: true,
  appearance: 'dark',

  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
    ['link', { rel: 'icon', type: 'image/png', sizes: '32x32', href: '/marks/png/favicon-32.png' }],
    ['link', { rel: 'apple-touch-icon', href: '/marks/png/favicon-180.png' }],
    ['meta', { name: 'theme-color', content: '#0E0E12' }],
    ['link', { rel: 'preconnect', href: 'https://fonts.googleapis.com' }],
    ['link', { rel: 'preconnect', href: 'https://fonts.gstatic.com', crossorigin: '' }],
    ['link', {
      rel: 'stylesheet',
      href: 'https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500;600;700;800&display=swap',
    }],
    // Social card
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:title', content: 'RigSmith — one rig, four tools' }],
    ['meta', { property: 'og:image', content: 'https://rigsmith.dev/marks/png/og-rigsmith.png' }],
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
    ['meta', { name: 'twitter:image', content: 'https://rigsmith.dev/marks/png/og-rigsmith.png' }],
  ],

  themeConfig: {
    logo: '/marks/rigsmith.svg',
    siteTitle: 'RigSmith',

    nav: [
      { text: 'rig', link: '/rig/', activeMatch: '^/rig/' },
      { text: 'changeRig', link: '/changerig/', activeMatch: '^/changerig/' },
      { text: 'shipRig', link: '/shiprig/', activeMatch: '^/shiprig/' },
      { text: 'claudeRig', link: '/clauderig/', activeMatch: '^/clauderig/' },
      { text: 'core', link: '/core/', activeMatch: '^/core/' },
      { text: 'Compare', link: '/compare/changesets', activeMatch: '^/compare/' },
    ],

    sidebar: {
      '/rig/': [
        {
          text: 'rig — dev launcher',
          items: [
            { text: 'Overview', link: '/rig/' },
            { text: 'Verbs', link: '/rig/verbs' },
            { text: 'Configuration', link: '/rig/configuration' },
          ],
        },
      ],
      '/changerig/': [
        {
          text: 'changeRig — changesets',
          items: [
            { text: 'Overview', link: '/changerig/' },
            { text: 'The lifecycle', link: '/changerig/lifecycle' },
          ],
        },
      ],
      '/shiprig/': [
        {
          text: 'shipRig — releases',
          items: [
            { text: 'Overview', link: '/shiprig/' },
            { text: 'The release pipeline', link: '/shiprig/pipeline' },
          ],
        },
      ],
      '/clauderig/': [
        {
          text: 'claudeRig — Claude Code sync',
          items: [
            { text: 'Overview', link: '/clauderig/' },
            { text: 'Commands', link: '/clauderig/commands' },
          ],
        },
      ],
      '/core/': [
        {
          text: 'core — the engine',
          items: [
            { text: 'Overview', link: '/core/' },
            { text: 'Plugin protocol', link: '/core/plugin-protocol' },
          ],
        },
      ],
      '/compare/': [
        {
          text: 'Compare',
          items: [
            { text: 'vs @changesets/cli', link: '/compare/changesets' },
            { text: 'vs make / just', link: '/compare/task-runners' },
          ],
        },
      ],
      '/guide/': [
        {
          text: 'Guide',
          items: [
            { text: 'Installation', link: '/guide/installation' },
          ],
        },
      ],
    },

    socialLinks: [{ icon: 'github', link: GITHUB }],

    editLink: {
      pattern: `${GITHUB}/edit/main/site/:path`,
      text: 'Edit this page on GitHub',
    },

    search: { provider: 'local' },

    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Single, statically-linked Go binaries — no runtime to install.',
    },
  },

  sitemap: { hostname: 'https://rigsmith.dev' },
})
