// Command folders prints whether its build is a release and where home puts the org's folders and those of the product acme, for home's tests.
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
	own, _ := home.OrgDir()
	mark, _ := home.MigratedMark("acme", "config.yaml")
	fmt.Println(build.Released())
	fmt.Println(config)
	fmt.Println(data)
	fmt.Println(org)
	fmt.Println(own)
	fmt.Println(mark)
	fmt.Println(home.ID(home.Folder("acme")))
}
