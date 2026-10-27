module.exports = {
  content: ['./index.html'],
  darkMode: 'class',
  theme: {
    extend: {
      // Existing white foreground utilities follow the theme; fixed white controls use text-[#fff].
      textColor: { white: 'rgb(var(--c-fg) / <alpha-value>)' },
      colors: {
        // Theme colors are channel triples set in index.html, so the page follows the system light or dark setting.
        // Dark slate-500 is 4.0:1 on the dark panels, under the 4.5:1 small text needs, so it is lifted there.
        slate: {
          200: 'rgb(var(--c-slate-200) / <alpha-value>)',
          300: 'rgb(var(--c-slate-300) / <alpha-value>)',
          400: 'rgb(var(--c-slate-400) / <alpha-value>)',
          500: 'rgb(var(--c-slate-500) / <alpha-value>)',
          600: 'rgb(var(--c-slate-600) / <alpha-value>)',
        },
        brand: {
          50: '#ecfdf5',
          100: '#d1fae5',
          300: '#6ee7b7',
          400: '#34d399',
          500: '#10b981',
          600: '#059669',
          700: '#047857',
          950: 'rgb(var(--c-brand-950) / <alpha-value>)',
        },
        dark: {
          950: 'rgb(var(--c-dark-950) / <alpha-value>)',
          900: 'rgb(var(--c-dark-900) / <alpha-value>)',
          850: 'rgb(var(--c-dark-850) / <alpha-value>)',
          800: 'rgb(var(--c-dark-800) / <alpha-value>)',
          750: 'rgb(var(--c-dark-750) / <alpha-value>)',
          700: 'rgb(var(--c-dark-700) / <alpha-value>)',
          600: 'rgb(var(--c-dark-600) / <alpha-value>)',
        },
        accent: {
          cyan: '#06B6D4',
          violet: '#8B5CF6',
          amber: '#F59E0B',
        },
      },
      fontFamily: {
        sans: ['Inter', 'Inter Fallback', 'system-ui', '-apple-system', 'sans-serif'],
        mono: ['JetBrains Mono', 'JetBrains Fallback', 'Fira Code', 'monospace'],
      },
    },
  },
};
