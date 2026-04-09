package imagegen

import "fmt"

const promptPrefix = "Cubist editorial illustration of "

const promptSuffix = ". Figures seen through a true cubist refraction — faces reveal front and profile " +
	"at the same moment, an eye where the cheekbone sits, noses split across two viewpoints. " +
	"Bodies dismantled and reassembled across the picture plane: anatomically wrong in the " +
	"parts, compositionally right in the whole. " +
	"Objects and forms are subject to the same refraction — edges split, volumes seen " +
	"from contradictory angles simultaneously, shapes that are geometrically impossible " +
	"yet immediately legible. " +
	"Bold dark outlines on flat color planes. Warm palette: cream, amber, " +
	"terracotta, burnt orange with accents of cerulean blue and sage green. " +
	"Painterly brushwork. Playful and warm, Apple corporate illustration energy. " +
	"Edge-to-edge composition filling the entire canvas, no border, no margin, no frame. " +
	"No text. Horizontal 4:3 aspect ratio."

// AssemblePrompt builds the full image generation prompt from a topic fragment.
func AssemblePrompt(topicFragment string) string {
	return promptPrefix + topicFragment + promptSuffix
}

// MetaPromptParts holds the system and user portions of the meta-prompt
// sent to Claude Sonnet for topic fragment generation.
type MetaPromptParts struct {
	System string
	User   string
}

const metaSystem = `You generate image prompt fragments for a cubist editorial illustration series. Each prompt describes a metaphorical scene with abstract figures consecrating a system design concept. Study the examples carefully — match their style, specificity, and structure. Output ONLY the prompt fragment, nothing else.`

const metaExamples = `Examples:

Title: "Rate Limiter"
→ an abstract figure as a calm gatekeeper, body assembled from geometric planes seen from multiple simultaneous perspectives in the style of Picasso. The figure gently directs a stream of colorful geometric shapes through a narrow passage with one hand raised.

Title: "News Feed"
→ abstract figures sharing and reading overlapping pages and glowing screens, bodies assembled from geometric planes seen from multiple simultaneous perspectives in the style of Picasso. Figures lean together, content flowing between them.

Title: "Chat System"
→ abstract figures in animated conversation, bodies assembled from geometric planes seen from multiple simultaneous perspectives in the style of Picasso. Speech bubbles and message fragments float between the figures as angular, overlapping shapes.`

// MetaPrompt returns the system and user prompt parts for generating
// a topic fragment from a question title via Claude Sonnet.
func MetaPrompt(questionTitle string) MetaPromptParts {
	return MetaPromptParts{
		System: metaSystem,
		User:   fmt.Sprintf("%s\n\nNow generate a prompt fragment for:\nTitle: %q", metaExamples, questionTitle),
	}
}
