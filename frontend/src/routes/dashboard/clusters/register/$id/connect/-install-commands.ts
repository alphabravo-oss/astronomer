export const AGENT_BOOTSTRAP_APPLY =
  'kubectl apply --server-side --field-manager=astronomer-bootstrap -f -';

export type CurlVariant = 'public_ca' | 'private_ca' | 'insecure';

export function inlineManifestCommand(manifest: string): string {
  return `cat <<'EOF' | ${AGENT_BOOTSTRAP_APPLY}\n${manifest}\nEOF`;
}

export function registrationCurlCommands(
  manifestURL: string,
  caURL: string,
): Record<CurlVariant, string> {
  if (!manifestURL) {
    return { public_ca: '', private_ca: '', insecure: '' };
  }
  return {
    public_ca: `curl -sfL ${manifestURL} | ${AGENT_BOOTSTRAP_APPLY}`,
    private_ca: `curl -sfL ${caURL} -o /tmp/astronomer-ca.crt\ncurl --cacert /tmp/astronomer-ca.crt -sfL ${manifestURL} | ${AGENT_BOOTSTRAP_APPLY}`,
    insecure: `curl --insecure -sfL ${manifestURL} | ${AGENT_BOOTSTRAP_APPLY}`,
  };
}
