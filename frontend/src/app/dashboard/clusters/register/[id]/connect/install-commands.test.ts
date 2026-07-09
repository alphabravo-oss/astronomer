import {
  AGENT_BOOTSTRAP_APPLY,
  inlineManifestCommand,
  registrationCurlCommands,
} from './install-commands';

describe('agent bootstrap install commands', () => {
  it('uses the single server-side field manager in every UI variant', () => {
    const commands = registrationCurlCommands(
      'https://astronomer.example/api/v1/register/token.yaml',
      'https://astronomer.example/api/v1/register/ca.crt',
    );
    const variants = [inlineManifestCommand('kind: List'), ...Object.values(commands)];

    expect(AGENT_BOOTSTRAP_APPLY).toBe(
      'kubectl apply --server-side --field-manager=astronomer-bootstrap -f -',
    );
    for (const command of variants) {
      expect(command).toContain(AGENT_BOOTSTRAP_APPLY);
      expect(command).not.toContain('kubectl apply -f -');
      expect(command.match(/--field-manager=astronomer-bootstrap/g)).toHaveLength(1);
    }
  });

  it('does not render partial curl commands before the manifest URL exists', () => {
    expect(registrationCurlCommands('', 'https://astronomer.example/ca')).toEqual({
      public_ca: '',
      private_ca: '',
      insecure: '',
    });
  });
});
