.PHONY : clean all

all :
	gofmt -w .
	goimports -w .
	go vet ./... || exit 1
	golint -set_exit_status ./... || exit 2

	#rm -rf main
	#GOOS=linux go build example/main.go

clean :

