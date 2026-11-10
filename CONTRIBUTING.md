# Public source boundary

This repository contains the reusable UPS core: sensing, autonomous control,
USB/NUT, the native desktop application, standard network transports and
provider-neutral extension points. A public checkout must build and run
without a private repository, account or service.

Provider-specific server addresses, API routes, schema identifiers, enrollment
instructions, deployment evidence and release-feed configuration belong in
the separately maintained private integration repository. Do not copy them
into source, tests, examples, documentation, screenshots, filenames or release
artifacts published here. Use reserved example domains and synthetic fixtures
in public tests. Keep credentials out of both source repositories.

Public product naming is `ups-node`. The existing logo artwork and its required
provenance are an explicit exception; this does not permit private endpoints
or other provider references in the surrounding code or documentation.

Keep generic interfaces here and supply private profiles only in a separate
build workspace. Never apply a private overlay to the public working tree.
Private repositories must not have public publication remotes. Preserve
licenses, copyright notices and third-party attribution.

Before public publication, check the exact staged/committed files, filenames,
generated artifacts and their metadata against the private publication policy.
A clean working tree scan does not establish that Git history is clean.
Previously published references require a separately reviewed history cleanup;
do not rewrite shared history or force-push as part of routine source cleanup.

Changes to the shared core must keep local UPS operation independent of cloud
availability. Validate both the public build and the private composition when
changing their interface. Keep hardware acceptance distinct from local tests.
