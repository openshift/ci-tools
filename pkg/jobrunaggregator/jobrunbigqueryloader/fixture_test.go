package jobrunbigqueryloader

import (
	"context"

	"cloud.google.com/go/bigquery"
)

type fakeInserter struct {
	errorGenerator func(insertedContent) error

	// actuals
	insertedValues []insertedContent
}

type insertedContent struct {
	row      map[string]bigquery.Value
	insertID string
	err      error
}

func newInsertedContent(saver bigquery.ValueSaver) insertedContent {
	ret := insertedContent{}
	ret.row, ret.insertID, ret.err = saver.Save()
	return ret
}

var _ BigQueryInserter = &fakeInserter{}

func (f *fakeInserter) Put(ctx context.Context, src interface{}) error {
	// we want to panic so no caught error works right
	valueSaver := src.(bigquery.ValueSaver)
	currInsert := newInsertedContent(valueSaver)
	f.insertedValues = append(f.insertedValues, currInsert)

	if f.errorGenerator == nil {
		return nil
	}

	return f.errorGenerator(currInsert)
}

type expectedInsertion struct {
	row      map[string]bigquery.Value
	insertID *string
	err      *error
}

func (expected *expectedInsertion) matches(actual insertedContent) error{
	if

}
