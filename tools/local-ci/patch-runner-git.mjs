// The workspace shim must not suppress real fetches in secondary checkouts.
export const gitShimAnchor = '# actions/checkout probes the remote URL via config.';
export const gitShimGuard = `# Astronomer: scope the synthetic checkout to the candidate workspace.
local_ci_git_root="$(/usr/bin/git.real rev-parse --show-toplevel 2>/dev/null || pwd -P)"
if [[ -n "\${GITHUB_WORKSPACE:-}" && "$local_ci_git_root" != "\${GITHUB_WORKSPACE}" ]]; then
  exec /usr/bin/git.real "$@"
fi

`;

export function patchRunnerGit(source, version) {
  if (version !== '0.18.1') throw new Error(`Review the Git shim patch for run-local-ci ${version}`);
  // This source is itself a JavaScript template literal that emits Bash.
  const escapedGuard = gitShimGuard.replaceAll('${', '\\${');
  if (source.includes(escapedGuard + gitShimAnchor)) return source;
  if (source.split(gitShimAnchor).length !== 2) throw new Error('Unexpected run-local-ci Git shim implementation');
  return source.replace(gitShimAnchor, escapedGuard + gitShimAnchor);
}
