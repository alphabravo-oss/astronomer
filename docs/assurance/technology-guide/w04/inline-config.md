# Exact OCI empty inline configuration

The retained supplier diagnosis in `supplier-metadata-diagnosis/` identifies the same exact inline `{}` configuration in both upstream manifests. The resolver now binds those original bytes to their descriptor SHA256, size and OCI empty-object media type; it does not remove the inline field or recompute a subject. No replacement config blob request occurs. The signature-first consumer still verifies the original subject before parsing it.

`ArtifactConfigurationMatches` checks content binding, not JSON schema or publisher identity. Separately fetched configuration retains its caller-owned closed JSON interpretation. Inline configuration is limited to the exact empty object with matching original data; nonempty objects, arrays, mismatched bytes/digest/size, external URLs and other inline media are rejected. Layers remain external-content-only under the existing limits.

The revised signed and supplier metadata tests first ran against the unchanged production parser: both inline positive controls failed as expected, with the baseline frozen before/after. The new helper controls cover independent descriptor/config/inline inconsistencies and require inputs to remain unchanged. Actual corrected qualification is recorded separately after the frozen source gate executes.

Producer archive validation and offline closed-graph paths still reject inline configuration. Their separate exact-byte extension remains required before claiming an original supplier graph can traverse those paths. This resolver slice is neither a whole supplier transfer, vendor endorsement, database freshness nor measured scan-used qualification.
