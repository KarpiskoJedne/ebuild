// @ts-check
/** Docusaurus config for the ebuild documentation website. */
const config = {
  title: 'ebuild',
  tagline: 'Easy Build — universal, zero-config, enterprise-grade builds',
  url: 'https://ebuild.dev',
  baseUrl: '/',
  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'warn',
  favicon: 'img/favicon.ico',
  organizationName: 'ebuild',
  projectName: 'ebuild',
  i18n: { defaultLocale: 'en', locales: ['en'] },
  presets: [
    [
      'classic',
      {
        docs: { sidebarPath: require.resolve('./sidebars.js') },
        blog: false,
        theme: { customCss: require.resolve('./src/css/custom.css') },
      },
    ],
  ],
  themeConfig: {
    navbar: {
      title: 'ebuild',
      items: [
        { type: 'docSidebar', sidebarId: 'docs', position: 'left', label: 'Docs' },
        { href: 'https://github.com/ebuild/ebuild', label: 'GitHub', position: 'right' },
      ],
    },
    footer: {
      style: 'dark',
      copyright: `Copyright © 2026 ebuild contributors. Apache-2.0.`,
    },
  },
};

module.exports = config;
