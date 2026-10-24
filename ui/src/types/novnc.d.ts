declare module "novnc-rfb" {
  export type RFBDisconnectDetail = {
    clean: boolean;
  };

  export type RFBSecurityFailureDetail = {
    reason?: string;
  };

  export default class RFB {
    constructor(
      target: Element,
      url: string,
      options?: {
        shared?: boolean;
      }
    );
    scaleViewport: boolean;
    resizeSession: boolean;
    disconnect(): void;
    focus(options?: FocusOptions): void;
    sendKey(keysym: number, code: string, down: boolean): void;
    clipboardPasteFrom(text: string): void;
    addEventListener(type: "connect", listener: () => void): void;
    addEventListener(
      type: "disconnect",
      listener: (event: CustomEvent<RFBDisconnectDetail>) => void
    ): void;
    addEventListener(
      type: "securityfailure",
      listener: (event: CustomEvent<RFBSecurityFailureDetail>) => void
    ): void;
    addEventListener(type: "credentialsrequired", listener: () => void): void;
  }
}

declare module "novnc-keysym" {
  const KeyTable: Record<string, number>;
  export default KeyTable;
}
