package main

import (
	"fmt"
	"os"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

func loadProto(path string) (*protoregistry.Files, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("aippatchgen: read %s: %w", path, err)
	}
	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(body, &fds); err != nil {
		return nil, fmt.Errorf("aippatchgen: unmarshal FileDescriptorSet: %w", err)
	}
	files, err := protodesc.NewFiles(&fds)
	if err != nil {
		return nil, fmt.Errorf("aippatchgen: build files registry: %w", err)
	}
	return files, nil
}

func lookupMessage(files *protoregistry.Files, fullName string) (protoreflect.MessageDescriptor, error) {
	d, err := files.FindDescriptorByName(protoreflect.FullName(fullName))
	if err != nil {
		return nil, fmt.Errorf("aippatchgen: descriptor %q not found: %w", fullName, err)
	}
	md, ok := d.(protoreflect.MessageDescriptor)
	if !ok {
		return nil, fmt.Errorf("aippatchgen: descriptor %q is not a message", fullName)
	}
	return md, nil
}
