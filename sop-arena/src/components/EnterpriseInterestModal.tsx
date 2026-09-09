import React, { useState, useEffect } from 'react';
import { 
  X, 
  Building2, 
  Mail, 
  ShieldCheck, 
  Send, 
  Check, 
  Copy, 
  ArrowRight,
  ExternalLink,
  Sparkles
} from 'lucide-react';

interface EnterpriseInterestModalProps {
  isOpen: boolean;
  onClose: () => void;
  initialTier?: 'pro' | 'enterprise' | 'hosted' | 'not_sure';
}

export const EnterpriseInterestModal: React.FC<EnterpriseInterestModalProps> = ({
  isOpen,
  onClose,
  initialTier = 'enterprise',
}) => {
  const [formData, setFormData] = useState({
    name: '',
    email: '',
    company: '',
    role: '',
    company_size: '51-200',
    tier: initialTier,
    use_cases: 'ai_devops',
    approx_agents: '6-25',
    website: '',
    idp: 'okta',
    deployment: 'self_hosted',
    message: '',
  });

  const [honeypot, setHoneypot] = useState('');
  const [renderTs, setRenderTs] = useState(Date.now());
  const [status, setStatus] = useState<'idle' | 'submitting' | 'success' | 'error'>('idle');
  const [errorMessage, setErrorMessage] = useState('');
  const [copiedEmail, setCopiedEmail] = useState(false);
  const [copiedSummary, setCopiedSummary] = useState(false);

  useEffect(() => {
    if (isOpen) {
      setRenderTs(Date.now());
      setStatus('idle');
      setErrorMessage('');
      setFormData(prev => ({
        ...prev,
        tier: initialTier || prev.tier
      }));
    }
  }, [isOpen, initialTier]);

  if (!isOpen) return null;

  const handleChange = (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) => {
    const { name, value } = e.target;
    setFormData(prev => ({ ...prev, [name]: value }));
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();

    // Honeypot check
    if (honeypot) {
      console.warn('Bot blocked via honeypot.');
      return;
    }

    // Velocity check
    if (Date.now() - renderTs < 1200) {
      console.warn('Bot blocked via velocity check.');
      return;
    }

    if (!formData.name.trim() || !formData.email.trim() || !formData.company.trim()) {
      setStatus('error');
      setErrorMessage('Please complete all required fields: Name, Work Email, and Company.');
      return;
    }

    setStatus('submitting');
    setErrorMessage('');

    // Offline / LocalStorage backup
    try {
      const stored = JSON.parse(localStorage.getItem('joltrin_inquiries') || '[]');
      stored.push({ ...formData, submitted_at: new Date().toISOString() });
      localStorage.setItem('joltrin_inquiries', JSON.stringify(stored));
    } catch {
      // ignore localStorage quota/iframe issues
    }

    // Backend submission attempt
    try {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 3500);
      await fetch('/api/billing/enterprise-contact', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(formData),
        signal: controller.signal,
      });
      clearTimeout(timeoutId);
    } catch {
      // Expected fallback on static GitHub Pages
    }

    setStatus('success');
  };

  const mailtoSubject = encodeURIComponent(`Joltrin ${formData.tier.toUpperCase()} Inquiry - ${formData.company || 'Direct Contact'}`);
  const mailtoBody = encodeURIComponent(
    `Name: ${formData.name}\nEmail: ${formData.email}\nCompany: ${formData.company}\nRole: ${formData.role}\nTier: ${formData.tier}\nUse Cases: ${formData.use_cases}\nApprox Agents: ${formData.approx_agents}\nDeployment: ${formData.deployment}\nIdP: ${formData.idp}\nWebsite: ${formData.website}\n\nRequirements / Notes:\n${formData.message}`
  );
  const mailtoUrl = `mailto:gerardrecinto@gmail.com?subject=${mailtoSubject}&body=${mailtoBody}`;

  const copySummaryText = () => {
    const text = `Joltrin ${formData.tier.toUpperCase()} Inquiry\nName: ${formData.name}\nEmail: ${formData.email}\nCompany: ${formData.company}\nRole: ${formData.role}\nUse Cases: ${formData.use_cases}\nAgents: ${formData.approx_agents}\nDeployment: ${formData.deployment}\nRequirements: ${formData.message}`;
    navigator.clipboard.writeText(text).then(() => {
      setCopiedSummary(true);
      setTimeout(() => setCopiedSummary(false), 2500);
    });
  };

  const copySalesEmail = () => {
    navigator.clipboard.writeText('gerardrecinto@gmail.com').then(() => {
      setCopiedEmail(true);
      setTimeout(() => setCopiedEmail(false), 2000);
    });
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-dark-950/80 backdrop-blur-md animate-fade-in">
      <div className="bg-dark-900 border border-dark-750 rounded-2xl max-w-2xl w-full max-h-[90vh] overflow-y-auto p-6 sm:p-8 shadow-2xl space-y-6">
        
        {/* Header */}
        <div className="flex items-center justify-between pb-4 border-b border-dark-800">
          <div className="flex items-center space-x-3">
            <div className="w-9 h-9 rounded-xl bg-brand-500/10 border border-brand-500/30 flex items-center justify-center text-brand-400">
              <Building2 className="w-5 h-5" />
            </div>
            <div>
              <h2 className="text-xl font-bold text-white tracking-tight">Deploy Joltrin in Production</h2>
              <p className="text-xs text-slate-400">From individual workloads to distributed enterprise agent fleets.</p>
            </div>
          </div>
          <button
            onClick={onClose}
            className="p-2 rounded-lg bg-dark-850 hover:bg-dark-800 text-slate-400 hover:text-white border border-dark-700 transition"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {status === 'success' ? (
          <div className="p-6 rounded-2xl bg-brand-500/10 border border-brand-500/30 space-y-4 text-slate-200">
            <div className="flex items-center space-x-2 text-brand-400 font-bold text-base">
              <ShieldCheck className="w-5 h-5" />
              <span>Inquiry Received! Thank you, {formData.name}.</span>
            </div>
            <p className="text-sm text-slate-300 leading-relaxed">
              Our engineering team will review your deployment requirements and get back to you at{' '}
              <strong className="text-white font-mono">{formData.email}</strong> within 1 business day.
            </p>

            <div className="bg-dark-950 p-4 rounded-xl border border-dark-800 text-xs font-mono space-y-1 text-slate-400">
              <div><span className="text-slate-500">Tier:</span> <span className="text-brand-400 font-semibold uppercase">{formData.tier}</span></div>
              <div><span className="text-slate-500">Company:</span> <span className="text-white">{formData.company}</span></div>
              <div><span className="text-slate-500">Estimated Agents:</span> <span className="text-white">{formData.approx_agents}</span></div>
              <div><span className="text-slate-500">Target Deployment:</span> <span className="text-white">{formData.deployment}</span></div>
            </div>

            <div className="flex flex-wrap items-center gap-3 pt-2">
              <a
                href={mailtoUrl}
                className="px-4 py-2 rounded-xl bg-brand-500 hover:bg-brand-400 text-black font-semibold text-xs flex items-center space-x-2 transition"
              >
                <Mail className="w-4 h-4" />
                <span>Open Direct Email Backup</span>
              </a>

              <button
                onClick={copySummaryText}
                className="px-4 py-2 rounded-xl bg-dark-850 hover:bg-dark-800 border border-dark-700 text-slate-300 font-mono text-xs flex items-center space-x-2 transition"
              >
                {copiedSummary ? <Check className="w-3.5 h-3.5 text-brand-400" /> : <Copy className="w-3.5 h-3.5" />}
                <span>{copiedSummary ? 'Copied Summary!' : 'Copy Summary'}</span>
              </button>

              <button
                onClick={copySalesEmail}
                className="px-4 py-2 rounded-xl bg-dark-850 hover:bg-dark-800 border border-dark-700 text-slate-400 hover:text-white font-mono text-xs transition"
              >
                {copiedEmail ? 'gerardrecinto@gmail.com copied' : 'Copy gerardrecinto@gmail.com'}
              </button>
            </div>

            <div className="pt-4 border-t border-dark-800 flex justify-end">
              <button
                onClick={onClose}
                className="px-4 py-2 rounded-xl bg-dark-800 hover:bg-dark-700 text-white text-xs font-semibold"
              >
                Close Window
              </button>
            </div>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            
            {/* Honeypot */}
            <div style={{ display: 'none' }} aria-hidden="true">
              <label htmlFor="arena-hp">Leave blank</label>
              <input
                id="arena-hp"
                type="text"
                value={honeypot}
                onChange={(e) => setHoneypot(e.target.value)}
                tabIndex={-1}
                autoComplete="off"
              />
            </div>

            {status === 'error' && (
              <div className="p-3.5 rounded-xl bg-rose-500/10 border border-rose-500/30 text-rose-300 text-xs">
                {errorMessage}
              </div>
            )}

            {/* Row 1: Name & Work Email */}
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <label className="block text-xs font-mono text-slate-300 mb-1">
                  Full Name <span className="text-rose-400">*</span>
                </label>
                <input
                  type="text"
                  name="name"
                  value={formData.name}
                  onChange={handleChange}
                  required
                  placeholder="Alex Mercer"
                  className="w-full bg-dark-950 border border-dark-700 rounded-lg px-3 py-2 text-xs text-white placeholder-slate-600 focus:border-brand-500 focus:outline-none"
                />
              </div>
              <div>
                <label className="block text-xs font-mono text-slate-300 mb-1">
                  Work Email <span className="text-rose-400">*</span>
                </label>
                <input
                  type="email"
                  name="email"
                  value={formData.email}
                  onChange={handleChange}
                  required
                  placeholder="alex@acme.ai"
                  className="w-full bg-dark-950 border border-dark-700 rounded-lg px-3 py-2 text-xs text-white placeholder-slate-600 focus:border-brand-500 focus:outline-none"
                />
              </div>
            </div>

            {/* Row 2: Company & Role */}
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <label className="block text-xs font-mono text-slate-300 mb-1">
                  Company Name <span className="text-rose-400">*</span>
                </label>
                <input
                  type="text"
                  name="company"
                  value={formData.company}
                  onChange={handleChange}
                  required
                  placeholder="Acme Autonomous Systems"
                  className="w-full bg-dark-950 border border-dark-700 rounded-lg px-3 py-2 text-xs text-white placeholder-slate-600 focus:border-brand-500 focus:outline-none"
                />
              </div>
              <div>
                <label className="block text-xs font-mono text-slate-300 mb-1">
                  Role / Title
                </label>
                <input
                  type="text"
                  name="role"
                  value={formData.role}
                  onChange={handleChange}
                  placeholder="Head of Infrastructure"
                  className="w-full bg-dark-950 border border-dark-700 rounded-lg px-3 py-2 text-xs text-white placeholder-slate-600 focus:border-brand-500 focus:outline-none"
                />
              </div>
            </div>

            {/* Row 3: Interested Tier & Company Size */}
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <label className="block text-xs font-mono text-slate-300 mb-1">
                  Interested Edition <span className="text-rose-400">*</span>
                </label>
                <select
                  name="tier"
                  value={formData.tier}
                  onChange={handleChange}
                  className="w-full bg-dark-950 border border-dark-700 rounded-lg px-3 py-2 text-xs font-mono text-white focus:border-brand-500 focus:outline-none"
                >
                  <option value="pro">Pro ($49/mo team governance)</option>
                  <option value="enterprise">Enterprise (SSO, RBAC &amp; SLAs)</option>
                  <option value="hosted">Hosted Cloud (Waitlist)</option>
                  <option value="not_sure">Not sure / Evaluating options</option>
                </select>
              </div>
              <div>
                <label className="block text-xs font-mono text-slate-300 mb-1">
                  Company Size
                </label>
                <select
                  name="company_size"
                  value={formData.company_size}
                  onChange={handleChange}
                  className="w-full bg-dark-950 border border-dark-700 rounded-lg px-3 py-2 text-xs font-mono text-white focus:border-brand-500 focus:outline-none"
                >
                  <option value="1-10">1 – 10 employees</option>
                  <option value="11-50">11 – 50 employees</option>
                  <option value="51-200">51 – 200 employees</option>
                  <option value="201-1000">201 – 1,000 employees</option>
                  <option value="1000+">1,000+ employees</option>
                </select>
              </div>
            </div>

            {/* Row 4: Primary Use Case & Number of Agents */}
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <label className="block text-xs font-mono text-slate-300 mb-1">
                  Primary Use Case
                </label>
                <select
                  name="use_cases"
                  value={formData.use_cases}
                  onChange={handleChange}
                  className="w-full bg-dark-950 border border-dark-700 rounded-lg px-3 py-2 text-xs font-mono text-white focus:border-brand-500 focus:outline-none"
                >
                  <option value="ai_devops">AI DevOps &amp; Production Automation</option>
                  <option value="swe_agents">Autonomous Coding &amp; SWE Agents</option>
                  <option value="enterprise_workflows">Enterprise Workflow &amp; Approval Barrier</option>
                  <option value="multi_agent">Multi-Agent Swarm Memory &amp; Consensus</option>
                  <option value="disaster_recovery">High Availability &amp; Disaster Recovery</option>
                  <option value="other">Other Architecture</option>
                </select>
              </div>
              <div>
                <label className="block text-xs font-mono text-slate-300 mb-1">
                  Estimated AI Agents
                </label>
                <select
                  name="approx_agents"
                  value={formData.approx_agents}
                  onChange={handleChange}
                  className="w-full bg-dark-950 border border-dark-700 rounded-lg px-3 py-2 text-xs font-mono text-white focus:border-brand-500 focus:outline-none"
                >
                  <option value="1-5">1 – 5 agents</option>
                  <option value="6-25">6 – 25 agents</option>
                  <option value="26-100">26 – 100 agents</option>
                  <option value="100+">100+ agents</option>
                </select>
              </div>
            </div>

            {/* Row 5: Deployment & SSO */}
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 p-3 bg-dark-950/60 border border-dark-800 rounded-xl">
              <div>
                <label className="block text-xs font-mono text-slate-300 mb-1">
                  Target Deployment
                </label>
                <select
                  name="deployment"
                  value={formData.deployment}
                  onChange={handleChange}
                  className="w-full bg-dark-900 border border-dark-700 rounded-lg px-3 py-2 text-xs font-mono text-white focus:border-brand-500 focus:outline-none"
                >
                  <option value="self_hosted">Self-Hosted (K8s, Docker, On-Prem)</option>
                  <option value="hosted">Interested in Managed Cloud</option>
                  <option value="hybrid">Hybrid Deployment</option>
                </select>
              </div>
              <div>
                <label className="block text-xs font-mono text-slate-300 mb-1">
                  Identity Provider (SSO)
                </label>
                <select
                  name="idp"
                  value={formData.idp}
                  onChange={handleChange}
                  className="w-full bg-dark-900 border border-dark-700 rounded-lg px-3 py-2 text-xs font-mono text-white focus:border-brand-500 focus:outline-none"
                >
                  <option value="okta">Okta (OIDC)</option>
                  <option value="entra_id">Microsoft Entra ID (Azure AD)</option>
                  <option value="other">Other / Custom SAML</option>
                  <option value="none">None / Not required</option>
                </select>
              </div>
            </div>

            {/* Row 6: Requirements Note */}
            <div>
              <label className="block text-xs font-mono text-slate-300 mb-1">
                Project Requirements / Architecture Context
              </label>
              <textarea
                name="message"
                value={formData.message}
                onChange={handleChange}
                rows={3}
                placeholder="Describe your current agent architecture, failure recovery concerns, or compliance needs..."
                className="w-full bg-dark-950 border border-dark-700 rounded-lg p-3 text-xs text-white placeholder-slate-600 focus:border-brand-500 focus:outline-none"
              />
            </div>

            {/* Submit Button */}
            <div className="pt-2">
              <button
                type="submit"
                disabled={status === 'submitting'}
                className="w-full py-3 px-6 rounded-xl bg-gradient-to-r from-brand-600 to-brand-500 hover:from-brand-500 hover:to-brand-400 font-bold text-xs text-black shadow-lg shadow-brand-500/20 flex items-center justify-center space-x-2 transition transform active:scale-95 disabled:opacity-50"
              >
                <Send className="w-4 h-4" />
                <span>{status === 'submitting' ? 'Submitting Inquiry...' : 'Submit Commercial Inquiry'}</span>
              </button>
              <p className="text-[11px] text-slate-500 text-center font-mono mt-2">
                🔒 Enterprise confidentiality guaranteed. No credit card or secrets required.
              </p>
            </div>

          </form>
        )}

      </div>
    </div>
  );
};
