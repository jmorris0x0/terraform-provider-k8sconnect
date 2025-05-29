Decide nested object name. cluster_connection is kind of verbose.

Test that token refresh works. This will be important for long applies. How to test for this?

Some kind of multi-cluster test? Not sure how to accomplish this.



Failure test for missing CRD?

Reasses trivial tests

CICD release process with artifacts and dependabot. Go releaser?

Warning for kind install

Handle if attempting to write a new resource that's already managed by the provider.

Fix terraform version issue in makefile. It should be installing automatically during build but isn't.

Make provider version DRY with build flag?

Parameterize kind version in makefile for build/test matrix

Revisit create/delete timeout decisions

Clean up acceptance test output

Fix wrong user in commits

We have no drift detection in Read()


Do we have metadata for the yaml split yet? Would be useful for errors.

https://claude.ai/chat/2301902a-c7ad-4daf-89ba-a6d5fd064b69




Add structured diff. This would be a good feature. Does this mean I have to put yaml in the state? Is it already?

We are just applying actual text currently. Maybe marshal to json early and applying that.

Suppress whitespace changes? After or during structured diffs?
