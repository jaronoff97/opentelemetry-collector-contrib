// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package ottl // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/ottl"

import (
	"fmt"

	"go.opentelemetry.io/collector/component"
	"go.uber.org/zap"
)

// Safeguard to statically ensure the Parser.ParseStatements method can be reflectively
// invoked by the ottlParserWrapper.parseStatements
var _ interface {
	ParseStatements(statements []string) ([]*Statement[any], error)
} = (*Parser[any])(nil)

// Safeguard to statically ensure any ParsedStatementConverter method can be reflectively
// invoked by the statementsConverterWrapper.call
var _ ParsedStatementConverter[any, any] = func(
	_ *ParserCollection[any],
	_ StatementsGetter,
	_ []*Statement[any],
) (any, error) {
	return nil, nil
}

// StatementsGetter represents a set of statements to be parsed.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
type StatementsGetter interface {
	// GetStatements retrieves the OTTL statements to be parsed
	GetStatements() []string
}

type defaultStatementsGetter []string

func (d defaultStatementsGetter) GetStatements() []string {
	return d
}

// NewStatementsGetter creates a new StatementsGetter.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
func NewStatementsGetter(statements []string) StatementsGetter {
	return defaultStatementsGetter(statements)
}

// Safeguard to statically ensure the Parser.ParseConditions method can be reflectively
// invoked by the ottlParserWrapper.parseConditions
var _ interface {
	ParseConditions(conditions []string) ([]*Condition[any], error)
} = (*Parser[any])(nil)

// Safeguard to statically ensure any ParsedConditionConverter method can be reflectively
// invoked by the conditionsConverterWrapper.call
var _ ParsedConditionConverter[any, any] = func(
	_ *ParserCollection[any],
	_ ConditionsGetter,
	_ []*Condition[any],
) (any, error) {
	return nil, nil
}

// ConditionsGetter represents a set of conditions to be parsed.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
type ConditionsGetter interface {
	// GetConditions retrieves the OTTL conditions to be parsed
	GetConditions() []string
}

type defaultConditionsGetter []string

func (d defaultConditionsGetter) GetConditions() []string {
	return d
}

// NewConditionsGetter creates a new ConditionsGetter.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
func NewConditionsGetter(statements []string) ConditionsGetter {
	return defaultConditionsGetter(statements)
}

// ParserCollection is a configurable set of ottl.Parser that can handle multiple OTTL contexts
// parsings, inferring the context, choosing the right parser for the given statements, and
// transforming the parsed ottl.Statement[K] slice into a common result of type R.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
type ParserCollection[R any] struct {
	contextParsers            map[string]*ParserCollectionConverter[R]
	contextInferrer           contextInferrer
	contextInferrerCandidates map[string]*priorityContextInferrerCandidate
	candidatesLowerContexts   map[string][]string
	modifiedStatementLogging  bool
	modifiedConditionLogging  bool
	Settings                  component.TelemetrySettings
	ErrorMode                 ErrorMode
}

// ParserCollectionOption is a configurable ParserCollection option.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
type ParserCollectionOption[R any] func(*ParserCollection[R]) error

// NewParserCollection creates a new ParserCollection.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
func NewParserCollection[R any](
	settings component.TelemetrySettings,
	options ...ParserCollectionOption[R],
) (*ParserCollection[R], error) {
	contextInferrerCandidates := map[string]*priorityContextInferrerCandidate{}
	pc := &ParserCollection[R]{
		Settings:                  settings,
		contextParsers:            map[string]*ParserCollectionConverter[R]{},
		contextInferrer:           newPriorityContextInferrer(settings, contextInferrerCandidates),
		contextInferrerCandidates: contextInferrerCandidates,
		candidatesLowerContexts:   map[string][]string{},
	}

	for _, op := range options {
		err := op(pc)
		if err != nil {
			return nil, err
		}
	}

	return pc, nil
}

// ParsedStatementConverter is a function that converts the parsed ottl.Statement[K] into
// a common representation to all parser collection contexts passed through WithParserCollectionContext.
// Given each parser has its own transform context type, they must agree on a common type [R]
// so it can be returned by the ParserCollection.ParseStatements and ParserCollection.ParseStatementsWithContext
// functions.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
type ParsedStatementConverter[K any, R any] func(collection *ParserCollection[R], statements StatementsGetter, parsedStatements []*Statement[K]) (R, error)

// ParsedConditionConverter is a function that converts the parsed ottl.Condition[K] into
// a common representation to all parser collection contexts passed through WithParserCollectionContext.
// Given each parser has its own transform context type, they must agree on a common type [R]
// so it can be returned by the ParserCollection.ParseStatements and ParserCollection.ParseStatementsWithContext
// functions.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
type ParsedConditionConverter[K any, R any] func(collection *ParserCollection[R], conditions ConditionsGetter, parsedConditions []*Condition[K]) (R, error)

func newNopParsedStatementConverter[K any]() ParsedStatementConverter[K, any] {
	return func(
		_ *ParserCollection[any],
		_ StatementsGetter,
		parsedStatements []*Statement[K],
	) (any, error) {
		return parsedStatements, nil
	}
}

// ParserCollectionContextOption is a configurable ParserCollectionContext option.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
type ParserCollectionContextOption[K, R any] func(*ParserCollectionConverter[R], *Parser[K])

type ParseConditionsWithContext[R any] func(collection *ParserCollection[R], context string, conditions ConditionsGetter, prependPathsContext bool) (R, error)
type ParseStatementsWithContext[R any] func(collection *ParserCollection[R], context string, statements StatementsGetter, prependPathsContext bool) (R, error)
type ParserCollectionConverter[R any] struct {
	statementsConverter ParseStatementsWithContext[R]
	conditionsConverter ParseConditionsWithContext[R]
}

func WithConditionConverter[K any, R any](converter ParsedConditionConverter[K, R]) ParserCollectionContextOption[K, R] {
	return func(pcp *ParserCollectionConverter[R], parser *Parser[K]) {
		pcp.conditionsConverter = func(pc *ParserCollection[R], context string, conditions ConditionsGetter, prependPathsContext bool) (R, error) {
			var err error
			var parsingConditions []string
			if prependPathsContext {
				originalConditions := conditions.GetConditions()
				parsingConditions := make([]string, 0, len(originalConditions))
				for _, cond := range originalConditions {
					prependedCondition, prependErr := parser.prependContextToConditionPaths(context, cond)
					if prependErr != nil {
						err = prependErr
						break
					}
					parsingConditions = append(parsingConditions, prependedCondition)
				}
				if err != nil {
					return *new(R), err
				}
				if pc.modifiedConditionLogging {
					pc.logModifications(originalConditions, parsingConditions)
				}
			} else {
				parsingConditions = conditions.GetConditions()
			}
			parsedConditions, err := parser.ParseConditions(parsingConditions)
			if err != nil {
				return *new(R), err
			}
			return converter(
				pc,
				conditions,
				parsedConditions,
			)
		}
	}
}

func WithStatementConverter[K any, R any](converter ParsedStatementConverter[K, R]) ParserCollectionContextOption[K, R] {
	return func(pcp *ParserCollectionConverter[R], parser *Parser[K]) {
		pcp.statementsConverter = func(pc *ParserCollection[R], context string, statements StatementsGetter, prependPathsContext bool) (R, error) {
			var err error
			var parsingStatements []string
			if prependPathsContext {
				originalStatements := statements.GetStatements()
				parsingStatements := make([]string, 0, len(originalStatements))
				for _, cond := range originalStatements {
					prependedStatement, prependErr := parser.prependContextToStatementPaths(context, cond)
					if prependErr != nil {
						err = prependErr
						break
					}
					parsingStatements = append(parsingStatements, prependedStatement)
				}
				if err != nil {
					return *new(R), err
				}
				if pc.modifiedStatementLogging {
					pc.logModifications(originalStatements, parsingStatements)
				}
			} else {
				parsingStatements = statements.GetStatements()
			}
			parsedStatements, err := parser.ParseStatements(parsingStatements)
			if err != nil {
				return *new(R), err
			}
			return converter(
				pc,
				statements,
				parsedStatements,
			)
		}
	}
}

// WithParserCollectionContext configures an ottl.Parser for the given context.
// The provided ottl.Parser must be configured to support the provided context using
// the ottl.WithPathContextNames option.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
func WithParserCollectionContext[K any, R any](
	context string,
	parser *Parser[K],
	opts ...ParserCollectionContextOption[K, R],
) ParserCollectionOption[R] {
	return func(mp *ParserCollection[R]) error {
		if _, ok := parser.pathContextNames[context]; !ok {
			return fmt.Errorf(`context "%s" must be a valid "%T" path context name`, context, parser)
		}
		pcp := &ParserCollectionConverter[R]{}
		for _, o := range opts {
			o(pcp, parser)
		}
		mp.contextParsers[context] = pcp

		for lowerContext := range parser.pathContextNames {
			if lowerContext != context {
				mp.candidatesLowerContexts[lowerContext] = append(mp.candidatesLowerContexts[lowerContext], context)
			}
		}

		mp.contextInferrerCandidates[context] = &priorityContextInferrerCandidate{
			hasEnumSymbol: func(enum *EnumSymbol) bool {
				_, err := parser.enumParser(enum)
				return err == nil
			},
			hasFunctionName: func(name string) bool {
				_, ok := parser.functions[name]
				return ok
			},
			getLowerContexts: mp.getLowerContexts,
		}
		return nil
	}
}

func (pc *ParserCollection[R]) getLowerContexts(context string) []string {
	return pc.candidatesLowerContexts[context]
}

// WithParserCollectionErrorMode has no effect on the ParserCollection, but might be used
// by the ParsedStatementConverter functions to handle/create StatementSequence.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
func WithParserCollectionErrorMode[R any](errorMode ErrorMode) ParserCollectionOption[R] {
	return func(tp *ParserCollection[R]) error {
		tp.ErrorMode = errorMode
		return nil
	}
}

// EnableParserCollectionModifiedStatementLogging controls the statements modification logs.
// When enabled, it logs any statements modifications performed by the parsing operations,
// instructing users to rewrite the statements accordingly.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
func EnableParserCollectionModifiedStatementLogging[R any](enabled bool) ParserCollectionOption[R] {
	return func(tp *ParserCollection[R]) error {
		tp.modifiedStatementLogging = enabled
		return nil
	}
}

// ParseStatements parses the given statements into [R] using the configured context's ottl.Parser
// and subsequently calling the ParsedStatementConverter function.
// The statement's context is automatically inferred from the [Path.Context] values, choosing the
// highest priority context found.
// If no contexts are present in the statements, or if the inferred value is not supported by
// the [ParserCollection], it returns an error.
// If parsing the statements fails, it returns the underlying [ottl.Parser.ParseStatements] error.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
func (pc *ParserCollection[R]) ParseStatements(statements StatementsGetter) (R, error) {
	statementsValues := statements.GetStatements()
	inferredContext, err := pc.contextInferrer.infer(statementsValues)
	if err != nil {
		return *new(R), err
	}

	if inferredContext == "" {
		return *new(R), fmt.Errorf("unable to infer context from statements, path's first segment must be a valid context name: %+q, and at least one context must be capable of parsing all statements: %+q", pc.supportedContextNames(), statementsValues)
	}

	_, ok := pc.contextParsers[inferredContext]
	if !ok {
		return *new(R), fmt.Errorf(`context "%s" inferred from the statements %+q is not a supported context: %+q`, inferredContext, statementsValues, pc.supportedContextNames())
	}

	return pc.ParseStatementsWithContext(inferredContext, statements, false)
}

// ParseStatementsWithContext parses the given statements into [R] using the configured
// context's ottl.Parser and subsequently calling the ParsedStatementConverter function.
// Unlike ParseStatements, it uses the provided context and does not infer it
// automatically. The context value must be supported by the [ParserCollection],
// otherwise an error is returned.
// If the statement's Path does not provide their Path.Context value, the prependPathsContext
// argument should be set to true, so it rewrites the statements prepending the missing paths
// contexts.
// If parsing the statements fails, it returns the underlying [ottl.Parser.ParseStatements] error.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
func (pc *ParserCollection[R]) ParseStatementsWithContext(context string, statements StatementsGetter, prependPathsContext bool) (R, error) {
	contextParser, ok := pc.contextParsers[context]
	if !ok {
		return *new(R), fmt.Errorf(`unknown context "%s" for stataments: %v`, context, statements.GetStatements())
	} else if contextParser.statementsConverter == nil {
		return *new(R), fmt.Errorf("no statements converter has been set")
	}
	return contextParser.statementsConverter(
		pc,
		context,
		statements,
		prependPathsContext,
	)
}

// EnableParserCollectionModifiedConditionLogging controls the conditions modification logs.
// When enabled, it logs any conditions modifications performed by the parsing operations,
// instructing users to rewrite the conditions accordingly.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
func EnableParserCollectionModifiedConditionLogging[R any](enabled bool) ParserCollectionOption[R] {
	return func(tp *ParserCollection[R]) error {
		tp.modifiedConditionLogging = enabled
		return nil
	}
}

// ParseConditions parses the given conditions into [R] using the configured context's ottl.Parser
// and subsequently calling the ParsedConditionConverter function.
// The condition's context is automatically inferred from the [Path.Context] values, choosing the
// highest priority context found.
// If no contexts are present in the conditions, or if the inferred value is not supported by
// the [ParserCollection], it returns an error.
// If parsing the conditions fails, it returns the underlying [ottl.Parser.ParseConditions] error.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
func (pc *ParserCollection[R]) ParseConditions(conditions ConditionsGetter) (R, error) {
	conditionsValues := conditions.GetConditions()
	inferredContext, err := pc.contextInferrer.infer(conditionsValues)
	if err != nil {
		return *new(R), err
	}

	if inferredContext == "" {
		return *new(R), fmt.Errorf("unable to infer context from conditions, path's first segment must be a valid context name: %+q, and at least one context must be capable of parsing all conditions: %+q", pc.supportedContextNames(), conditionsValues)
	}

	_, ok := pc.contextParsers[inferredContext]
	if !ok {
		return *new(R), fmt.Errorf(`context "%s" inferred from the conditions %+q is not a supported context: %+q`, inferredContext, conditionsValues, pc.supportedContextNames())
	}

	return pc.ParseConditionsWithContext(inferredContext, conditions, false)
}

// ParseConditionsWithContext parses the given conditions into [R] using the configured
// context's ottl.Parser and subsequently calling the ParsedConditionConverter function.
// Unlike ParseConditions, it uses the provided context and does not infer it
// automatically. The context value must be supported by the [ParserCollection],
// otherwise an error is returned.
// If the condition's Path does not provide their Path.Context value, the prependPathsContext
// argument should be set to true, so it rewrites the conditions prepending the missing paths
// contexts.
// If parsing the conditions fails, it returns the underlying [ottl.Parser.ParseConditions] error.
//
// Experimental: *NOTE* this API is subject to change or removal in the future.
func (pc *ParserCollection[R]) ParseConditionsWithContext(context string, conditions ConditionsGetter, prependPathsContext bool) (R, error) {
	contextParser, ok := pc.contextParsers[context]
	if !ok {
		return *new(R), fmt.Errorf(`unknown context "%s" for stataments: %v`, context, conditions.GetConditions())
	} else if contextParser.conditionsConverter == nil {
		return *new(R), fmt.Errorf("no conditions converter has been set")
	}

	return contextParser.conditionsConverter(
		pc,
		context,
		conditions,
		prependPathsContext,
	)
}

func (pc *ParserCollection[R]) logModifications(originalStatements, modifiedStatements []string) {
	var fields []zap.Field
	for i, original := range originalStatements {
		if modifiedStatements[i] != original {
			statementKey := fmt.Sprintf("[%v]", i)
			fields = append(fields, zap.Dict(
				statementKey,
				zap.String("original", original),
				zap.String("modified", modifiedStatements[i])),
			)
		}
	}
	if len(fields) > 0 {
		pc.Settings.Logger.Info("one or more statements were modified to include their paths context, please rewrite them accordingly", zap.Dict("statements", fields...))
	}
}

func (pc *ParserCollection[R]) supportedContextNames() []string {
	contextsNames := make([]string, 0, len(pc.contextParsers))
	for k := range pc.contextParsers {
		contextsNames = append(contextsNames, k)
	}
	return contextsNames
}
