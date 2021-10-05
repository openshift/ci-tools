import React from "react";
import {Alert, AlertGroup} from "@patternfly/react-core";

type MessageProps = {
  messages?: string[],
  message?: string
}

export const ErrorMessage: (props: MessageProps) => (JSX.Element) = (props: MessageProps) => {
  if (props.messages && props.messages.length > 0) {
    return (
      <AlertGroup>
        {props.messages.map((message, i) => {
          return <Alert key={"error_" + i} variant="danger" title={message}/>
        })}
      </AlertGroup>
    );
  } else if (props.message && props.message.trim()) {
    return (
      <Alert variant="danger" title={props.message}/>
    )
  }
  return <div/>
}

export const SuccessMessage: (props: MessageProps) => (JSX.Element) = (props: MessageProps) => {
  if (props.messages && props.messages.length > 0) {
    return (
      <AlertGroup>
        {props.messages.map((message, i) => {
          return <Alert key={"success_" + i} variant="success" title={message}/>
        })}
      </AlertGroup>
    );
  } else if (props.message && props.message.trim()) {
    return (
      <Alert variant="success" title={props.message}/>
    )
  }
  return <div/>
}
