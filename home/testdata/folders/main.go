// Command folders prints whether its build is a release and where home puts the folders of the product acme, for home's tests.
package main

import (
	"fmt"

	"github.com/tugbayatilla/mankit/v4/build"
	"github.com/tugbayatilla/mankit/v4/home"
)

func main() {
	config, _ := home.Product("acme")
	data, _ := home.ProductData("acme")
	org, _ := home.Dir()
	mark, _ := home.MigratedMark("acme", "config.yaml")
	fmt.Println(build.Released())
	fmt.Println(config)
	fmt.Println(data)
	fmt.Println(org)
	fmt.Println(mark)
	fmt.Println(home.ID(home.Folder("acme")))
}
