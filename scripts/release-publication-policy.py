#!/usr/bin/env python3
"""Historical deferrals are immutable tag scopes; future tags use strict approval."""
import json
from pathlib import Path

POLICY = Path(__file__).resolve().parents[1] / 'deploy/release/publication-policy.json'

def load(path=POLICY):
    data=json.loads(path.read_text())
    expected={'schema_version','historical_automated_tags','historical_deferred_qualifications','future_default','maintenance_automated_policy','upstream_policy'}
    if set(data)!=expected or data['schema_version']!=1 or data['historical_automated_tags']!=['v1.2.0','v1.2.1']:
        raise ValueError('release policy violates historical tag scope')
    if data['historical_deferred_qualifications']!=['license','cloud','scale','rancher','human_accessibility']:
        raise ValueError('historical qualifications changed')
    if data['future_default']!={'contract':'strict_signed_release_approval','deferred_qualifications':[], 'license_qualification':'passed','vulnerability_qualification':'passed','fips_claim':'requires_separate_validated_module_evidence','accessibility_claim':'requires_separate_human_qualification'}:
        raise ValueError('future release policy is not strict')
    maintenance=data['maintenance_automated_policy']
    if set(maintenance)!={'status','applicable_tags','required_decision'} or maintenance['status']!='not_approved' or maintenance['applicable_tags'] or not maintenance['required_decision'].strip():
        raise ValueError('maintenance policy has no approval')
    if data['upstream_policy']!='adopt_fixed_newer_upstream_otherwise_exact_expiring_waiver_no_fork':
        raise ValueError('upstream policy changed')
    return data

def require_historical_automated(tag):
    if tag not in load()['historical_automated_tags']:
        raise ValueError('automated publication exception applies only to v1.2.0 and v1.2.1')

if __name__=='__main__':
    load()
    print('Release publication policy: PASS (future strict approval; maintenance policy not approved)')
