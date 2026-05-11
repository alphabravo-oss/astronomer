/**
 * UI-only metadata for each Dex connector type. The authoritative list of
 * fields lives on the backend (`/connector-types/`) — this file just augments
 * those records with friendly labels, descriptions, icons, and placeholder
 * text so the wizard can render without a second round-trip.
 *
 * Anything not listed here falls back to a sensible default (the field name
 * humanised, no placeholder). Adding a new connector type only requires the
 * backend registry change; the UI will still render — just without the icon
 * sugar — until this file is updated.
 */
import {
  Building2,
  Globe,
  Github,
  GitMerge,
  Box,
  Chrome,
  KeyRound,
  Lock,
  Server,
  ShieldCheck,
  Network,
  type LucideIcon,
} from 'lucide-react';

export interface ConnectorTypeMeta {
  label: string;
  description: string;
  icon: LucideIcon;
  /** Per-field UI hints. Key matches the backend registry's field name. */
  fields?: Record<string, FieldMeta>;
}

export interface FieldMeta {
  label?: string;
  placeholder?: string;
  helper?: string;
  /** Render as a textarea (e.g. PEM-encoded CA data, multi-line lists). */
  multiline?: boolean;
  /** Field expects a comma-separated list. We split/join for the user. */
  list?: boolean;
}

export const CONNECTOR_META: Record<string, ConnectorTypeMeta> = {
  microsoft: {
    label: 'Azure AD / Entra ID',
    description: 'Microsoft Entra ID (Azure Active Directory) tenant.',
    icon: Building2,
    fields: {
      tenant: { label: 'Tenant ID', placeholder: '00000000-0000-0000-0000-000000000000' },
      clientID: { label: 'Application (client) ID' },
      clientSecret: { label: 'Client secret' },
      groups: { label: 'Allowed groups', list: true, helper: 'Comma-separated group object IDs' },
    },
  },
  oidc: {
    label: 'OpenID Connect',
    description: 'Generic OIDC — Keycloak, Authentik, Auth0, Cognito, etc.',
    icon: KeyRound,
    fields: {
      issuer: { placeholder: 'https://idp.example.com' },
      clientID: { label: 'Client ID' },
      clientSecret: { label: 'Client secret' },
      scopes: { list: true, placeholder: 'openid, profile, email, groups' },
    },
  },
  okta: {
    label: 'Okta',
    description: 'Okta (uses OIDC under the hood).',
    icon: ShieldCheck,
    fields: {
      issuer: { placeholder: 'https://your-tenant.okta.com' },
      clientID: { label: 'Client ID' },
      clientSecret: { label: 'Client secret' },
      groups: { list: true, helper: 'Restrict to these Okta groups' },
    },
  },
  github: {
    label: 'GitHub',
    description: 'GitHub OAuth — gate by org / team membership.',
    icon: Github,
    fields: {
      clientID: { label: 'Client ID' },
      clientSecret: { label: 'Client secret' },
      orgs: {
        label: 'Allowed orgs',
        list: true,
        helper: 'Comma-separated GitHub org logins',
        placeholder: 'astronomer, alphabravo',
      },
      teams: { label: 'Allowed teams', list: true, helper: 'org/team-slug entries' },
    },
  },
  gitlab: {
    label: 'GitLab',
    description: 'GitLab.com or self-hosted.',
    icon: GitMerge,
    fields: {
      baseURL: { placeholder: 'https://gitlab.com' },
      clientID: { label: 'Application ID' },
      clientSecret: { label: 'Application secret' },
      groups: { list: true, helper: 'Comma-separated GitLab group paths' },
    },
  },
  bitbucket: {
    label: 'Bitbucket',
    description: 'Bitbucket Cloud OAuth.',
    icon: Box,
    fields: {
      clientID: { label: 'Key' },
      clientSecret: { label: 'Secret' },
      teams: { list: true },
    },
  },
  google: {
    label: 'Google Workspace',
    description: 'Google Workspace / Cloud Identity.',
    icon: Chrome,
    fields: {
      clientID: { label: 'Client ID', placeholder: 'xxxxx.apps.googleusercontent.com' },
      clientSecret: { label: 'Client secret' },
      hostedDomains: {
        label: 'Hosted domains',
        list: true,
        placeholder: 'example.com, example.org',
        helper: 'Restrict logins to these Workspace domains',
      },
    },
  },
  saml: {
    label: 'SAML 2.0',
    description: 'ADFS, Shibboleth, Okta-SAML, generic SAML IdPs.',
    icon: Lock,
    fields: {
      ssoURL: { label: 'SSO URL', placeholder: 'https://idp.example.com/sso/saml' },
      entityIssuer: { label: 'Entity issuer', placeholder: 'urn:astronomer:dex' },
      caData: { label: 'IdP CA (PEM)', multiline: true, helper: 'Paste the IdP signing certificate' },
      ca: { label: 'IdP CA path', helper: 'Alternative to CA data — file path inside Dex pod' },
      usernameAttr: { label: 'Username attribute', placeholder: 'name' },
      emailAttr: { label: 'Email attribute', placeholder: 'email' },
      groupsAttr: { label: 'Groups attribute', placeholder: 'groups' },
    },
  },
  ldap: {
    label: 'LDAP / Active Directory',
    description: 'Bind-and-search against an LDAP / AD directory.',
    icon: Server,
    fields: {
      host: { placeholder: 'ldap.example.com:636' },
      bindDN: { label: 'Bind DN', placeholder: 'cn=dex,ou=service,dc=example,dc=com' },
      bindPW: { label: 'Bind password' },
      rootCAData: { label: 'Root CA (PEM)', multiline: true },
      'userSearch.baseDN': { placeholder: 'ou=users,dc=example,dc=com' },
      'userSearch.username': { placeholder: 'uid' },
      'userSearch.idAttr': { placeholder: 'uid' },
      'userSearch.emailAttr': { placeholder: 'mail' },
      'userSearch.nameAttr': { placeholder: 'cn' },
    },
  },
  oauth: {
    label: 'Generic OAuth 2.0',
    description: 'Any OAuth 2.0 provider that exposes a userinfo endpoint.',
    icon: Network,
    fields: {
      clientID: { label: 'Client ID' },
      clientSecret: { label: 'Client secret' },
      tokenURL: { placeholder: 'https://idp.example.com/oauth/token' },
      authorizationURL: { placeholder: 'https://idp.example.com/oauth/authorize' },
      userInfoURL: { placeholder: 'https://idp.example.com/oauth/userinfo' },
      scopes: { list: true },
    },
  },
};

const DEFAULT_META: ConnectorTypeMeta = {
  label: '',
  description: 'Custom connector.',
  icon: Globe,
};

export function getConnectorMeta(type: string): ConnectorTypeMeta {
  return CONNECTOR_META[type] ?? { ...DEFAULT_META, label: type };
}

/** Humanise a registry field name. `clientID` -> "Client ID". */
export function humaniseFieldName(name: string): string {
  return name
    // Insert space before camelCase boundaries.
    .replace(/([a-z])([A-Z])/g, '$1 $2')
    // Initial cap.
    .replace(/^(.)/, (m) => m.toUpperCase())
    // Common acronyms.
    .replace(/\bId\b/g, 'ID')
    .replace(/\bUrl\b/g, 'URL')
    .replace(/\bUri\b/g, 'URI')
    .replace(/\bDn\b/g, 'DN')
    .replace(/\bPw\b/g, 'PW')
    .replace(/\bCa\b/g, 'CA');
}
