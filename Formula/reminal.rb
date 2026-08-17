class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "3.0.5"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.5/reminal_3.0.5_darwin_arm64.tar.gz"
      sha256 "805ad2cbcfc51707df2005ea34d23b032d9d619ad8fea2b0c7ace8725f89c050"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.5/reminal_3.0.5_darwin_amd64.tar.gz"
      sha256 "12fa07da2fd70e0bda338fff147b3c2bc02c0e1095359747a14c796845337128"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.5/reminal_3.0.5_linux_arm64.tar.gz"
      sha256 "650d9bd5055a4161cb757eb9ac44a629d34bc4c95d0e275ca7b6f7c43dab023e"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.5/reminal_3.0.5_linux_amd64.tar.gz"
      sha256 "9b4440f9a967a9eafafd342ee4e59ac1b0a4fea7c2aa884a82f299f13f549c24"
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
