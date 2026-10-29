import { pathToFileURL } from 'node:url';

// Wrangler publishes a single minimum Node version. Reject unexpected metadata
// rather than trying to evaluate arbitrary package text or guessing at a range.
export function supportsRuntime(version, requirement) {
  const minimum = /^>=(\d+)\.(\d+)\.(\d+)$/.exec(requirement.trim());
  const current = /^(\d+)\.(\d+)\.(\d+)$/.exec(version);
  if (!minimum || !current) throw new Error('Expected a stable Node version and a single >=x.y.z engine requirement.');
  for (let i = 1; i <= 3; i++) {
    const difference = Number(current[i]) - Number(minimum[i]);
    if (difference !== 0) return difference > 0;
  }
  return true;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const requirement = process.argv[2] || '';
    if (!supportsRuntime(process.versions.node, requirement)) {
      throw new Error(`Node ${process.versions.node} does not meet the deploy CLI requirement ${requirement}.`);
    }
    console.log(`Node ${process.versions.node} meets ${requirement}. No package code was downloaded or executed.`);
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
