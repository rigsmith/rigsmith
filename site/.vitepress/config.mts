import { defineConfig } from 'vitepress'

// The site is keyed by binary name: /rig/, /changerig/, /relrig/, /clauderig/,
// plus /core/ (the engine) and /compare/. Each tool section gets its own
// sidebar, selected by path prefix below. See docs/WEBSITE.md for the plan.

const GITHUB = 'https://github.com/JohnCampionJr/rigsmith'

export default defineConfig({
  title: 'rigsmith',
  description:
    'A family of convention-first, zero-runtime-dependency CLI tools for polyglot monorepos.',
  lang: 'en-US',
  cleanUrls: true,
  lastUpdated: true,

  head: [
    ['link', { rel: 'icon', href: '/favicon.svg', type: 'image/svg+xml' }],
    ['meta', { name: 'theme-color', content: '#3c7d5b' }],
  ],

  themeConfig: {
    siteTitle: 'rigsmith',

    nav: [
      { text: 'rig', link: '/rig/', activeMatch: '^/rig/' },
      { text: 'changerig', link: '/changerig/', activeMatch: '^/changerig/' },
      { text: 'relrig', link: '/relrig/', activeMatch: '^/relrig/' },
      { text: 'clauderig', link: '/clauderig/', activeMatch: '^/clauderig/' },
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
          text: 'changerig — changesets',
          items: [
            { text: 'Overview', link: '/changerig/' },
            { text: 'The lifecycle', link: '/changerig/lifecycle' },
          ],
        },
      ],
      '/relrig/': [
        {
          text: 'relrig — releases',
          items: [
            { text: 'Overview', link: '/relrig/' },
            { text: 'The release pipeline', link: '/relrig/pipeline' },
          ],
        },
      ],
      '/clauderig/': [
        {
          text: 'clauderig — Claude Code sync',
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
