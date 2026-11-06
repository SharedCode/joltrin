import { handleContact } from './contact.js';

export default {
  async fetch(request, env, ctx) {
    const { pathname } = new URL(request.url);
    if (pathname === '/api/contact') return handleContact(request, env);
    return env.ASSETS.fetch(request);
  },
};
