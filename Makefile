.PHONY: fmt build test vet race smoke check
fmt build test vet race smoke check:
	bash scripts/dev.sh $@
