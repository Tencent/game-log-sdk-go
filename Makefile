.PHONY : clean all

all :
	gofmt -w .
	goimports -w .
	go vet ./... || exit 1
	golint -set_exit_status ./... || exit 2

	rm -rf test/test
	GOOS=linux go build -o test/test test/test.go

clean :

