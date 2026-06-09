import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'Palena Pseudonymizer',
  description:
    'Pseudonymization guardrail for the LiteLLM proxy — mask real entity names before the LLM, restore them in the response.',
  cleanUrls: true,
  ignoreDeadLinks: [/\/LICENSE$/],

  head: [
    ['link', { rel: 'icon', type: 'image/png', href: '/favicon.png' }],
    ['meta', { property: 'og:image', content: '/og-image.png' }],
  ],

  themeConfig: {
    logo: '/palena-icon.png',

    nav: [
      { text: 'Guide', link: '/guide/what-is-it' },
      { text: 'Reference', link: '/reference/http-protocol' },
      {
        text: 'GitHub',
        link: 'https://github.com/PalenaAI/palena-litellm-pseudonymizer',
      },
    ],

    sidebar: [
      {
        text: 'Introduction',
        items: [
          { text: 'What is it?', link: '/guide/what-is-it' },
          { text: 'How it works', link: '/guide/how-it-works' },
          { text: 'Getting started', link: '/guide/getting-started' },
        ],
      },
      {
        text: 'Guides',
        items: [
          { text: 'Configuration', link: '/guide/configuration' },
          { text: 'Organization detection', link: '/guide/organization-detection' },
          { text: 'Deploy with Helm', link: '/guide/deployment' },
          { text: 'Wire into LiteLLM', link: '/guide/litellm-integration' },
        ],
      },
      {
        text: 'Reference',
        items: [
          { text: 'HTTP protocol', link: '/reference/http-protocol' },
          { text: 'Environment variables', link: '/reference/environment' },
        ],
      },
    ],

    socialLinks: [
      { icon: 'github', link: 'https://github.com/PalenaAI/palena-litellm-pseudonymizer' },
    ],

    footer: {
      message: 'Released under the Apache 2.0 License.',
      copyright: 'Copyright © 2026 bitkaio LLC',
    },

    search: {
      provider: 'local',
    },
  },
})
