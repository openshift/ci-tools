import React from "react";
import ReactDOM from "react-dom";
import App from '@app/index';
import { store } from '@app/store'
import { Provider } from 'react-redux'

if (process.env.NODE_ENV !== "production") {
  const config = {
    rules: [
      {
        id: 'color-contrast',
        enabled: false
      }
    ]
  };
  // eslint-disable-next-line @typescript-eslint/no-var-requires, no-undef
  const axe = require("react-axe");
  axe(React, ReactDOM, 1000, config);
}

ReactDOM.render(<Provider store={store}><App /></Provider>, document.getElementById("root") as HTMLElement);
