Decide nested object name.

Test that token refresh works. This might be important for long applies.

Add structured diff. This would be a great feature. Does this mean I have to put yaml in the state? Is it already?

We are just applying actual text currently. I think I should be marshalling to json early and applying that.

Suppress whitespace changes? After or during structured diffs.

Failure test for missing CRD?

Reasses trivial tests

CICD release process with artifacts and dependabot. Automated releases?

Warning for kind install

Handle if attempting to write a new resource that's already managed by the provider.

Fix terraform version issue in makefile

Make provider version DRY with build flag?

Parameterize kind version in makefile for build/test matrix

Revisit create/delete timeout decisions
