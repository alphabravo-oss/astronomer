/**
 * Tests for the kind-specific ResourceOverview branches (plan A4 / C1).
 *
 * ResourceOverview is a pure presentational component, so we render it
 * directly with raw-k8s-object fixtures and assert the tailored sections.
 * The full ResourceDetail shell (which pulls in PodTerminal/wterm) is exercised
 * by the Playwright e2e instead.
 */

import React from 'react';
import { render, screen } from '@testing-library/react';

// resource-detail statically imports PodTerminal, which pulls in the @wterm/react
// ESM bundle that jest can't transform. We never render the terminal here, so
// stub the module out. (e2e covers the real terminal path.)
jest.mock('@/components/workloads/pod-terminal', () => ({ PodTerminal: () => null }));

import { ResourceOverview } from '@/components/resources/resource-detail';

describe('ResourceOverview kind-specific branches', () => {
  it('renders a pod overview with phase, node, IP and per-container rows', () => {
    render(
      <ResourceOverview
        resourceType="pods"
        obj={{
          metadata: { name: 'my-pod', namespace: 'default' },
          spec: {
            nodeName: 'node-1',
            containers: [{ name: 'app', image: 'nginx:1.25' }],
          },
          status: {
            phase: 'Running',
            podIP: '10.1.2.3',
            containerStatuses: [
              { name: 'app', ready: true, restartCount: 2, state: { running: {} } },
            ],
          },
        }}
      />
    );

    expect(screen.getByText('Pod')).toBeInTheDocument();
    expect(screen.getByText('Running')).toBeInTheDocument();
    expect(screen.getByText('node-1')).toBeInTheDocument();
    expect(screen.getByText('10.1.2.3')).toBeInTheDocument();
    // Per-container row: name + image + state.
    expect(screen.getByText('app')).toBeInTheDocument();
    expect(screen.getByText('nginx:1.25')).toBeInTheDocument();
    expect(screen.getByText('running')).toBeInTheDocument();
  });

  it('renders a service overview with type, clusterIP, ports and selector', () => {
    render(
      <ResourceOverview
        resourceType="services"
        obj={{
          metadata: { name: 'my-svc', namespace: 'default' },
          spec: {
            type: 'ClusterIP',
            clusterIP: '10.0.0.10',
            selector: { app: 'web' },
            ports: [{ name: 'http', port: 80, targetPort: 8080, protocol: 'TCP' }],
          },
        }}
      />
    );

    expect(screen.getByText('Service')).toBeInTheDocument();
    expect(screen.getByText('ClusterIP')).toBeInTheDocument();
    expect(screen.getByText('10.0.0.10')).toBeInTheDocument();
    expect(screen.getByText('Ports')).toBeInTheDocument();
    expect(screen.getByText('8080')).toBeInTheDocument();
    expect(screen.getByText('Selector')).toBeInTheDocument();
    expect(screen.getByText('web')).toBeInTheDocument();
  });

  it('lists configmap keys', () => {
    render(
      <ResourceOverview
        resourceType="configmaps"
        obj={{
          metadata: { name: 'cm', namespace: 'default' },
          data: { 'config.yaml': 'a: 1', 'app.properties': 'x=1' },
        }}
      />
    );

    expect(screen.getByText('Keys')).toBeInTheDocument();
    // Keys appear in both the kind-specific "Keys" list and the generic "Data" table.
    expect(screen.getAllByText('config.yaml').length).toBeGreaterThan(0);
    expect(screen.getAllByText('app.properties').length).toBeGreaterThan(0);
  });

  it('renders ingress class, hosts and TLS', () => {
    render(
      <ResourceOverview
        resourceType="ingresses"
        obj={{
          metadata: { name: 'ing', namespace: 'default' },
          spec: {
            ingressClassName: 'nginx',
            rules: [{ host: 'app.example.com' }],
            tls: [{ hosts: ['app.example.com'], secretName: 'tls' }],
          },
        }}
      />
    );

    expect(screen.getByText('Ingress')).toBeInTheDocument();
    expect(screen.getByText('nginx')).toBeInTheDocument();
    expect(screen.getByText('Hosts')).toBeInTheDocument();
    // host appears in both the Hosts list and the TLS list.
    expect(screen.getAllByText('app.example.com').length).toBeGreaterThan(0);
    expect(screen.getByText('TLS')).toBeInTheDocument();
  });

  it('renders PVC status, capacity, storageClass and volume', () => {
    render(
      <ResourceOverview
        resourceType="persistentvolumeclaims"
        obj={{
          metadata: { name: 'data', namespace: 'default' },
          spec: { storageClassName: 'gp3', volumeName: 'pv-1' },
          status: { phase: 'Bound', capacity: { storage: '10Gi' } },
        }}
      />
    );

    expect(screen.getByText('PersistentVolumeClaim')).toBeInTheDocument();
    expect(screen.getByText('Bound')).toBeInTheDocument();
    expect(screen.getByText('10Gi')).toBeInTheDocument();
    expect(screen.getByText('gp3')).toBeInTheDocument();
    expect(screen.getByText('pv-1')).toBeInTheDocument();
  });

  it('renders a job overview with completions, parallelism and counts', () => {
    render(
      <ResourceOverview
        resourceType="jobs"
        obj={{
          metadata: { name: 'backup', namespace: 'default' },
          spec: { completions: 3, parallelism: 2, backoffLimit: 4 } as never,
          status: { succeeded: 1, active: 2, failed: 0 } as never,
        }}
      />
    );
    expect(screen.getByText('Job')).toBeInTheDocument();
    expect(screen.getByText('completions')).toBeInTheDocument();
    expect(screen.getByText('1/3')).toBeInTheDocument();
    expect(screen.getByText('parallelism')).toBeInTheDocument();
  });

  it('renders a cronjob overview with schedule and suspend state', () => {
    render(
      <ResourceOverview
        resourceType="cronjobs"
        obj={{
          metadata: { name: 'nightly', namespace: 'default' },
          spec: { schedule: '0 3 * * *', suspend: true, concurrencyPolicy: 'Forbid' } as never,
          status: { active: [{ name: 'nightly-123' }] } as never,
        }}
      />
    );
    expect(screen.getByText('CronJob')).toBeInTheDocument();
    expect(screen.getByText('0 3 * * *')).toBeInTheDocument();
    expect(screen.getByText('suspended')).toBeInTheDocument();
    // active jobs count derived from the status.active array length.
    expect(screen.getByText('active jobs')).toBeInTheDocument();
  });

  it('renders an HPA overview with target, min/max and replicas', () => {
    render(
      <ResourceOverview
        resourceType="hpa"
        obj={{
          metadata: { name: 'web-hpa', namespace: 'default' },
          spec: {
            scaleTargetRef: { kind: 'Deployment', name: 'web' },
            minReplicas: 2, maxReplicas: 10,
            metrics: [{ type: 'Resource', resource: { name: 'cpu', target: { averageUtilization: 75 } } }],
          } as never,
          status: { currentReplicas: 3, desiredReplicas: 4 } as never,
        }}
      />
    );
    expect(screen.getByText('HorizontalPodAutoscaler')).toBeInTheDocument();
    expect(screen.getByText('Deployment web')).toBeInTheDocument();
    expect(screen.getByText('2 / 10')).toBeInTheDocument();
    expect(screen.getByText('Metrics')).toBeInTheDocument();
    expect(screen.getByText('target 75%')).toBeInTheDocument();
  });

  it('masks secret data values', () => {
    render(
      <ResourceOverview
        resourceType="secrets"
        obj={{
          metadata: { name: 'creds', namespace: 'default' },
          data: { password: 'c2VjcmV0' },
        }}
      />
    );

    // The key is shown but the value is masked, never the raw base64.
    expect(screen.getByText('password')).toBeInTheDocument();
    expect(screen.queryByText('c2VjcmV0')).not.toBeInTheDocument();
    expect(screen.getByText('••••••••')).toBeInTheDocument();
  });
});
