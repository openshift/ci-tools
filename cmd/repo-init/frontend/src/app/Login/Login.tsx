import React, {useContext, useEffect, useState} from "react";
import {AuthContext, ConfigPropertiesContext} from "@app/types";
import {GithubIcon} from "@patternfly/react-icons";
import Styled from "styled-components";
import {useHistory} from "react-router-dom";

const Login: React.FunctionComponent = () => {
  const authContext = useContext(AuthContext);
  const configPropertiesContext = useContext(ConfigPropertiesContext);
  const history = useHistory();
  const [data, setData] = useState({isLoading: false, errorMessage: ""});

  useEffect(() => {
    if (authContext.userData.isAuthenticated) {
      history.push('/repo-init');
    } else {
      // After requesting Github access, Github redirects back to your app with a code parameter
      const url = window.location.href;
      const hasCode = url.includes("?code=");

      // If Github API returns the code parameter
      if (hasCode) {
        const newUrl = url.split("?code=");
        window.history.pushState({}, "", newUrl[0]);
        setData({...data, isLoading: true, errorMessage: ""});

        const code = newUrl[1];

        // Request to exchange code for an access token
        fetch(process.env.REACT_APP_API_URI + `/auth`, {
          method: "POST",
          body: code,
        })
          .then((response) => response.json())
          .then((response) => {
            authContext.updateContext({
              ...authContext.userData,
              isAuthenticated: true,
              userName: response.userName,
              token: response.accessToken
            });
            history.push('/repo-init');
          })
          .catch(() => {
            setData({
              isLoading: false,
              errorMessage: "Sorry! Login failed"
            });
          });
      }
    }
  }, [data, authContext, history]);

  return (
    <Wrapper>
      <section className="container">
        <div>
          <h1>Welcome</h1>
          <span>Repo Onboarding Tool</span>
          <span>{data.errorMessage}</span>
          <div className="login-container">
            {data.isLoading ? (
              <div className="loader-container">
                <div className="loader"/>
              </div>
            ) : (
              <a
                className="login-link"
                href={`https://github.com/login/oauth/authorize?scope=user,repo&client_id=${configPropertiesContext.configProperties.githubClientId}&redirect_uri=${configPropertiesContext.configProperties.githubRedirectUrl}`}
                onClick={() => {
                  setData({...data, errorMessage: "", isLoading: true});
                }}
              >
                <GithubIcon/>
                <span>Login with GitHub</span>
              </a>
            )}
          </div>
        </div>
      </section>
    </Wrapper>
  )
}

const Wrapper = Styled.section`
  .container {
    display: flex;
    justify-content: center;
    align-items: center;
    height: 100vh;
    font-family: Arial;

    > div:nth-child(1) {
      display: flex;
      flex-direction: column;
      justify-content: center;
      align-items: center;
      box-shadow: 0 1px 4px 0 rgba(0, 0, 0, 0.2);
      transition: 0.3s;
      width: 25%;
      height: 45%;
      > h1 {
        font-size: 2rem;
        margin-bottom: 20px;
      }
      > span:nth-child(2) {
        font-size: 1.1rem;
        color: #808080;
        margin-bottom: 70px;
      }
      > span:nth-child(3) {
        margin: 10px 0 20px;
        color: red;
      }
      .login-container {
        background-color: #000;
        width: 70%;
        border-radius: 3px;
        color: #fff;
        display: flex;
        align-items: center;
        justify-content: center;
        > .login-link {
          text-decoration: none;
          color: #fff;
          text-transform: uppercase;
          cursor: default;
          display: flex;
          align-items: center;
          height: 40px;
          > span:nth-child(2) {
            margin-left: 5px;
          }
        }
        .loader-container {
          display: flex;
          justify-content: center;
          align-items: center;
          height: 40px;
        }
        .loader {
          border: 4px solid #f3f3f3;
          border-top: 4px solid #3498db;
          border-radius: 50%;
          width: 12px;
          height: 12px;
          animation: spin 2s linear infinite;
        }
        @keyframes spin {
          0% {
            transform: rotate(0deg);
          }
          100% {
            transform: rotate(360deg);
          }
        }
      }
    }
  }
`;


export {Login}
