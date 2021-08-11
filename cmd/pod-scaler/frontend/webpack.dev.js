const path = require('path');
const { merge } = require("webpack-merge");
const common = require("./webpack.common.js");
const { stylePaths } = require("./stylePaths");
const HOST = process.env.HOST || "localhost";
const PORT = process.env.PORT || "9000";

const API_ENDPOINT = process.env.API_ENDPOINT || "http://localhost:9000";

module.exports = merge(common('development'), {
  mode: "development",
  devtool: "eval-source-map",
  devServer: {
    contentBase: "./dist",
    host: HOST,
    port: PORT,
    compress: true,
    inline: true,
    historyApiFallback: true,
    overlay: true,
    open: true,
    proxy: {
      '/api': {
        target: API_ENDPOINT,
        secure: false,
      }
    }
  },
  module: {
    rules: [
      {
        test: /\.css$/,
        include: [
          ...stylePaths
        ],
        use: ["style-loader", "css-loader"]
      }
    ]
  }
});
