/**
 * Type-level guards binding the hand-written camelCase view types in
 * `src/types/index.ts` to the OpenAPI-generated wire types.
 *
 * WHY THIS EXISTS
 *
 * This API serialises snake_case (see ClusterResponse / BackupResponse /
 * HelmRepositoryResponse in internal/handler). The frontend's types are
 * camelCase, and the bridge is a single axios response interceptor that
 * rewrites every key on the way in (`camelizeKeys`, src/lib/camelize.ts).
 *
 * That bridge works, but it is invisible to the type system: `camelizeKeys<T>`
 * returns `T` unchanged as far as TypeScript is concerned, so a hand-written
 * camelCase interface can claim ANY field and the compiler will agree. The
 * catalog Repositories table shipped `chartCount: number` as a required field
 * for months. No column produced it, no query computed it, no OpenAPI schema
 * declared it — the table rendered a blank cell for every repository and
 * nothing failed, because the only description of that field was the interface
 * asserting it existed.
 *
 * The guard below closes that specific hole: a hand-written view type may not
 * declare a key that the camelized OpenAPI schema does not have. It compares
 * key SETS, not value types, deliberately — narrowing `repo_type?: string` to
 * a `HelmRepoType` union is a legitimate thing for a view type to do, while
 * inventing a field outright never is.
 */
import type { OpenAPIComponents } from '@/types/openapi.generated';
import type { HelmRepository, Cluster } from '@/types';

/** Type-level mirror of `snakeToCamel` in src/lib/camelize.ts. */
export type SnakeToCamel<S extends string> = S extends `${infer Head}_${infer Tail}`
  ? `${Head}${Capitalize<SnakeToCamel<Tail>>}`
  : S;

/** Type-level mirror of `camelizeKeys`: rewrites the key set of one object. */
export type CamelizeKeys<T> = {
  [K in keyof T as SnakeToCamel<Extract<K, string>>]: T[K];
};

/** Keys `Local` declares that the camelized `Wire` schema does not have. */
export type PhantomWireKeys<Local, Wire> = Exclude<keyof Local, keyof CamelizeKeys<Wire>>;

/**
 * Resolves to `true` when every key of `Local` exists on the camelized `Wire`
 * schema, and otherwise to a tuple naming the offenders — which fails to be
 * assignable from `true`, so the offending key appears in the compiler error.
 */
export type AssertNoPhantomWireKeys<Local, Wire> = [PhantomWireKeys<Local, Wire>] extends [never]
  ? true
  : ['field(s) absent from the OpenAPI schema:', PhantomWireKeys<Local, Wire>];

type WireSchemas = OpenAPIComponents['schemas'];

// --- Bound types -----------------------------------------------------------
//
// Add an entry here when a hand-written view type is meant to describe a
// documented API response. Each is a compile-time assertion: `tsc --noEmit`
// fails if the view type drifts ahead of the spec.

export const helmRepositoryMatchesWire: AssertNoPhantomWireKeys<
  HelmRepository,
  WireSchemas['HelmRepository']
> = true;

// TODO(wire-contract): `Cluster` does not pass this guard yet. Enabling it
// reports eight phantom fields:
//
//   health, namespaceCount, cpuCapacity, cpuUsage, memoryCapacity,
//   memoryUsage, directAccessEnabled, decommissioning
//
// These are not all the same problem — some look like client-side enrichment
// that belongs in a separate view type, but `directAccessEnabled` is read
// straight off the API object in components/clusters/edit-cluster-modal.tsx,
// and the API sends cpu_percentage / memory_percentage rather than the
// usage/capacity pair declared here. Untangling which are undocumented
// response fields (fix the spec) and which are genuinely absent (fix the
// type, and the screens reading them) is its own change; it is deliberately
// not bundled into the catalog chart_count fix.
//
//   export const clusterMatchesWire: AssertNoPhantomWireKeys<Cluster, WireSchemas['Cluster']> = true;
export type _ClusterWirePhantoms = PhantomWireKeys<Cluster, WireSchemas['Cluster']>;
