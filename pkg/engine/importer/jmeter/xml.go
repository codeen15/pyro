package jmeter

import (
	"encoding/xml"
	"strings"
)

// node is a generic XML element of a .jmx file.
type node struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Text     string     `xml:",chardata"`
	Children []node     `xml:",any"`
}

func (n *node) attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func (n *node) tag() string { return n.XMLName.Local }

// class is the element's test class (testclass, else its tag).
func (n *node) class() string {
	if c := n.attr("testclass"); c != "" {
		return c
	}
	return n.tag()
}

func (n *node) name() string     { return n.attr("testname") }
func (n *node) enabled() bool    { return n.attr("enabled") != "false" }
func (n *node) guiclass() string { return n.attr("guiclass") }

// prop returns a scalar property (stringProp, intProp, longProp, boolProp,
// doubleProp) by name.
func (n *node) prop(name string) string {
	for i := range n.Children {
		c := &n.Children[i]
		switch c.tag() {
		case "stringProp", "intProp", "longProp", "boolProp", "doubleProp", "floatProp":
			if c.attr("name") == name {
				return strings.TrimSpace(c.Text)
			}
		}
	}
	return ""
}

func (n *node) boolProp(name string) bool { return n.prop(name) == "true" }

// element returns a nested elementProp by name.
func (n *node) element(name string) *node {
	for i := range n.Children {
		c := &n.Children[i]
		if c.tag() == "elementProp" && c.attr("name") == name {
			return c
		}
	}
	return nil
}

// collection returns the items of a collectionProp by name.
func (n *node) collection(name string) []node {
	for i := range n.Children {
		c := &n.Children[i]
		if c.tag() == "collectionProp" && c.attr("name") == name {
			return c.Children
		}
	}
	return nil
}

// item is a test element and the hashTree of its children.
type item struct {
	el   *node
	kids []item
}

// tree converts a hashTree's children (element, hashTree, element,
// hashTree, …) into items.
func tree(ht *node) []item {
	var out []item
	for i := 0; i < len(ht.Children); i++ {
		c := &ht.Children[i]
		if c.tag() == "hashTree" {
			continue
		}
		it := item{el: c}
		if i+1 < len(ht.Children) && ht.Children[i+1].tag() == "hashTree" {
			it.kids = tree(&ht.Children[i+1])
			i++
		}
		out = append(out, it)
	}
	return out
}
