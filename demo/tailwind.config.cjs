module.exports = {
  content: ['./index.html'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        brand: {
          50: '#ecfdf5',
          100: '#d1fae5',
          400: '#34d399',
          500: '#10b981',
          600: '#059669',
          700: '#047857',
        },
        dark: {
          950: '#07090E',
          900: '#0B0F17',
          850: '#111726',
          800: '#182238',
          700: '#22304E',
          600: '#334155',
        },
        accent: {
          cyan: '#06B6D4',
          violet: '#8B5CF6',
          amber: '#F59E0B',
        },
      },
      fontFamily: {
        sans: ['Inter', 'system-ui', '-apple-system', 'sans-serif'],
        mono: ['JetBrains Mono', 'Fira Code', 'monospace'],
      },
    },
  },
};
