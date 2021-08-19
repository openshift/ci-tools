import { configureStore, createSlice, PayloadAction } from '@reduxjs/toolkit';
import { TreeViewDataItem } from '@patternfly/react-core';

const initialTrees: Record<string, TreeViewDataItem[]> = {};

export const trees = createSlice({
  name: 'trees',
  initialState: initialTrees,
  reducers: {
    updateTrees: (state, action: PayloadAction<Record<string, TreeViewDataItem[]>>) => {
      for (const name in action.payload) {
        if (action.payload.hasOwnProperty(name)) {
          state[name] = action.payload[name];
        }
      }
    },
  },
});

export const selectTrees = (state: RootState) => state.trees;
export const { updateTrees } = trees.actions;

const initialNames: Record<string, string> = {};

export const names = createSlice({
  name: 'names',
  initialState: initialNames,
  reducers: {
    updateNames: (state, action: PayloadAction<Record<string, string>>) => {
      for (const name in action.payload) {
        if (action.payload.hasOwnProperty(name)) {
          state[name] = action.payload[name];
        }
      }
    },
  },
});

export const selectNames = (state: RootState) => state.names;
export const { updateNames } = names.actions;

const initialParents: Record<string, string> = {};

export const parents = createSlice({
  name: 'parents',
  initialState: initialParents,
  reducers: {
    updateParents: (state, action: PayloadAction<Record<string, string>>) => {
      for (const name in action.payload) {
        if (action.payload.hasOwnProperty(name)) {
          state[name] = action.payload[name];
        }
      }
    },
  },
});

export const selectParents = (state: RootState) => state.parents;
export const { updateParents } = parents.actions;

export const store = configureStore({
  reducer: {
    trees: trees.reducer,
    names: names.reducer,
    parents: parents.reducer,
  },
});

export type RootState = ReturnType<typeof store.getState>;
export type AppDispatch = typeof store.dispatch;
