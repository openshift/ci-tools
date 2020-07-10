package main

import (
	"strings"

	dockercmd "github.com/openshift/imagebuilder/dockerfile/command"
	"github.com/openshift/imagebuilder/dockerfile/parser"
)

// https://github.com/openshift/builder/blob/6a52122d21e0528fbf014097d70770429fbc4448/pkg/build/builder/docker.go#L376
// error return removed because error was always nil and our linter rightfully complains about that being useless :)
func replaceLastFrom(node *parser.Node, image string, alias string) {
	if node == nil {
		return
	}
	for i := len(node.Children) - 1; i >= 0; i-- {
		child := node.Children[i]
		if child != nil && child.Value == dockercmd.From {
			if child.Next == nil {
				child.Next = &parser.Node{}
			}

			child.Next.Value = image
			if len(alias) != 0 {
				if child.Next.Next == nil {
					child.Next.Next = &parser.Node{}
				}
				child.Next.Next.Value = "as"
				if child.Next.Next.Next == nil {
					child.Next.Next.Next = &parser.Node{}
				}
				child.Next.Next.Next.Value = alias
			}
			return
		}
	}
}

func nodeHasFromRef(node *parser.Node) (string, bool) {
	for _, arg := range node.Flags {
		switch {
		case strings.HasPrefix(arg, "--from="):
			from := strings.TrimPrefix(arg, "--from=")
			return from, true
		}
	}
	return "", false
}
