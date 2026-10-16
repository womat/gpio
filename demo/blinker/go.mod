module github.com/womat/gpio/demo/blinker

go 1.27

require github.com/womat/gpio v1.0.0

require (
	github.com/warthog618/go-gpiocdev v0.9.2 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/womat/gpio => ../..
