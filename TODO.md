Decide nested object name.

Test that token refresh works. This might be important for long applies.

Add structured diff. This would be a great feature. Does this mean I have to put yaml in the state? Is it already?

Also suppress whitespace changes?

Multi-doc per yaml? Or a data resource to split them up?
No: Terraform expects each resource instance to map to one unit of state. Stuffing several Kubernetes objects into one resource means a single field change in one object forces the whole bundle to recreate, makes drift detection noisy, and breaks terraform import.


Templating like gavin bunny? Still useful for for_each? Probably.

Failure test for missing CRD?

Use real logs? What am I using now.

Reasses trivial tests

CICD release process with artifacts and dependabot. Automated releases?

Warning for kind install

Add go mod tidy to make build step
