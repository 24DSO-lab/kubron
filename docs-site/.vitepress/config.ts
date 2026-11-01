import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'Kubron',
  description: 'Fast, private Kubernetes desktop IDE — multi-cluster, Helm, GitOps, logs/exec/port-forward, YAML editor. Go + Wails + Vue.',
  lang: 'en-US',

  // Change to '/kubron/' if deploying to https://24dso-labs.github.io/kubron/
  // Leave as '/' for custom domain or Cloudflare Pages / project root.
  base: process.env.DOCS_BASE || '/',

  head: [
    ['link', { rel: 'icon', href: '/favicon.svg', type: 'image/svg+xml' }],
    ['meta', { name: 'theme-color', content: '#3b82f6' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:title', content: 'Kubron — Fast, private Kubernetes desktop IDE' }],
    ['meta', { property: 'og:description', content: 'Multi-cluster dashboard, Helm, GitOps, logs/exec/port-forward, YAML editor. No telemetry. Go + Wails + Vue.' }],
    ['meta', { property: 'og:image', content: '/og.png' }],
  ],

  locales: {
    root: {
      label: 'English',
      lang: 'en',
      themeConfig: {
        nav: [
          { text: 'Guide', link: '/guide/getting-started' },
          { text: 'Features', link: '/features/' },
          { text: 'Server', link: '/server/' },
          { text: 'Development', link: '/development/' },
          {
            text: 'Download',
            link: 'https://github.com/24DSO-labs/kubron/releases'
          }
        ],
        sidebar: {
          '/guide/': [
            {
              text: 'Guide',
              items: [
                { text: 'Getting Started', link: '/guide/getting-started' },
                { text: 'Installation', link: '/guide/installation' },
                { text: 'First cluster', link: '/guide/first-cluster' },
                { text: 'Keyboard shortcuts', link: '/guide/shortcuts' }
              ]
            }
          ],
          '/features/': [
            {
              text: 'Features',
              items: [
                { text: 'Overview', link: '/features/' },
                { text: 'Multi-cluster', link: '/features/multi-cluster' },
                { text: 'Dashboard', link: '/features/dashboard' },
                { text: 'Resources & YAML', link: '/features/resources' },
                { text: 'Helm', link: '/features/helm' },
                { text: 'GitOps', link: '/features/gitops' },
                { text: 'Security & RBAC', link: '/features/security' }
              ]
            }
          ],
          '/server/': [
            {
              text: 'Server mode',
              items: [
                { text: 'Overview', link: '/server/' },
                { text: 'Quick start', link: '/server/quick-start' },
                { text: 'Docker & Kubernetes', link: '/server/deploy' },
                { text: 'Configuration', link: '/server/configuration' }
              ]
            }
          ],
          '/development/': [
            {
              text: 'Development',
              items: [
                { text: 'Overview', link: '/development/' },
                { text: 'Architecture', link: '/development/architecture' },
                { text: 'Extending (pkg/ext)', link: '/development/extending' },
                { text: 'Contributing', link: '/development/contributing' },
                { text: 'Releasing', link: '/development/releasing' }
              ]
            }
          ]
        }
      }
    },
    ru: {
      label: 'Русский',
      lang: 'ru',
      link: '/ru/',
      themeConfig: {
        nav: [
          { text: 'Руководство', link: '/ru/guide/getting-started' },
          { text: 'Возможности', link: '/ru/features/' },
          { text: 'Сервер', link: '/ru/server/' },
          { text: 'Разработка', link: '/ru/development/' },
          {
            text: 'Скачать',
            link: 'https://github.com/24DSO-labs/kubron/releases'
          }
        ],
        sidebar: {
          '/ru/guide/': [
            {
              text: 'Руководство',
              items: [
                { text: 'Быстрый старт', link: '/ru/guide/getting-started' },
                { text: 'Установка', link: '/ru/guide/installation' }
              ]
            }
          ],
          '/ru/features/': [
            {
              text: 'Возможности',
              items: [
                { text: 'Обзор', link: '/ru/features/' }
              ]
            }
          ],
          '/ru/server/': [
            {
              text: 'Серверный режим',
              items: [
                { text: 'Обзор', link: '/ru/server/' }
              ]
            }
          ],
          '/ru/development/': [
            {
              text: 'Разработка',
              items: [
                { text: 'Обзор', link: '/ru/development/' }
              ]
            }
          ]
        }
      }
    }
  },

  themeConfig: {
    logo: '/logo.svg',
    siteTitle: 'Kubron',

    socialLinks: [
      { icon: 'github', link: 'https://github.com/24DSO-labs/kubron' }
    ],

    footer: {
      message: 'Released under the Apache-2.0 License.',
      copyright: 'Copyright © 2026 The Kubron Authors'
    },

    search: {
      provider: 'local'
    },

    editLink: {
      pattern: 'https://github.com/24DSO-labs/kubron/edit/master/docs-site/:path',
      text: 'Edit this page on GitHub'
    },

    lastUpdated: {
      text: 'Updated at',
      formatOptions: {
        dateStyle: 'medium',
        timeStyle: 'short'
      }
    },

    outline: {
      level: [2, 3]
    }
  },

  markdown: {
    lineNumbers: false
  },

  cleanUrls: true
})
