class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.10.0"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.0/reminal_1.10.0_darwin_arm64.tar.gz"
      sha256 "513fc905a5ea81c2f92494ff217497618aee7cd7fde07b75587a859c3e878cf6"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.0/reminal_1.10.0_darwin_amd64.tar.gz"
      sha256 "9898f1bcb6799af0e99edaf5c0f477eae61e8ba994586b87ad2f66c50feec1df"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.0/reminal_1.10.0_linux_arm64.tar.gz"
      sha256 "f3b7a9918c39c38454d66d489b4538c9e9f1956ea20f4f2bd536821e9b8bfcf0"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.0/reminal_1.10.0_linux_amd64.tar.gz"
      sha256 "73981705e36c6993f15071006179f89103181625ae74102057fab68cd1a5fc71"
    end
  end

  depends_on "go" => :build if build.head?

  def install
    if build.head?
      system "go", "build", "-ldflags=#{ldflags}", "-o", bin/"reminal", "./cmd/reminal"
      # Build the native window-capture helper from source when Xcode is present;
      # otherwise the window mirror falls back to screencapture.
      if OS.mac? && which("swiftc")
        system "swiftc", "-O", "-o", bin/"reminal-capture", "native/reminal-capture/main.swift"
      end
    else
      bin.install "reminal"
      # The darwin bottle bundles the ScreenCaptureKit capture helper next to the
      # binary; the agent auto-discovers it for the native window mirror.
      bin.install "reminal-capture" if File.exist?("reminal-capture")
    end
  end

  def ldflags
    "-s -w " \
      "-X main.version=#{version} " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudRelay=wss://reminal-relay.futuristic.workers.dev/ws " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudWeb=https://reminal-relay.futuristic.workers.dev"
  end

  def caveats
    <<~EOS
      reminal connects to the hosted relay automatically — no setup needed.

        reminal              # share your terminal
        reminal --connect ID --pin PIN
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/reminal version")
  end
end
