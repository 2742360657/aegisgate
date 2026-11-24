.PHONY: fmt build test vet race smoke check public
fmt build test vet race smoke check public:
	bash scripts/dev.sh $@
