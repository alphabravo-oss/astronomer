# Connecting Charlie to Astronomer

Astronomer follows the shared Charlie product pattern:

1. In Charlie, create an Astronomer deployment, attach the route and knowledge
   packs, then create a connection.
2. Copy the Charlie endpoint and one-time connect token.
3. In Astronomer, open **Settings → Charlie → Connection**, paste both values,
   validate, and confirm.

Astronomer verifies the token locally, installs the Charlie agent, and turns
Charlie surfaces on. It does not store a durable Charlie API key and does not
call Charlie central. An air-gapped package file remains available under the
same settings page.

The Charlie operator documentation describes how to issue and revoke the
one-time connect token; no Charlie source checkout is required to install this
Astronomer release.
